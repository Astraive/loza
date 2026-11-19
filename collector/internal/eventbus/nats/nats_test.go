package nats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/nats-io/nats.go/jetstream"
)

// Tests require a running NATS server. Use build tag for CI.
// go test -tags=integration ./internal/eventbus/nats/...

var errIteratorBoom = errors.New("iterator error")

// errorIterator mimics a persistently failing iter.Next() (e.g. a deleted
// consumer): it returns an error immediately on every call.
type errorIterator struct {
	calls atomic.Int32
}

func (i *errorIterator) Next(_ ...jetstream.NextOpt) (jetstream.Msg, error) {
	i.calls.Add(1)
	return nil, errIteratorBoom
}

func noopHandler(context.Context, eventbus.Message) error { return nil }

// TestConsumeMessagesBacksOffOnIteratorError verifies that repeated iterator
// errors trigger a capped exponential backoff instead of a tight retry loop.
func TestConsumeMessagesBacksOffOnIteratorError(t *testing.T) {
	closed := &atomic.Bool{}
	iter := &errorIterator{}
	ctx := context.Background()

	var backoffs []time.Duration
	stopAfter := 5
	sleep := func(_ context.Context, d time.Duration) bool {
		backoffs = append(backoffs, d)
		return len(backoffs) < stopAfter
	}

	const initial, max = 10 * time.Millisecond, 40 * time.Millisecond
	consumeMessages(ctx, closed, iter, "subject", noopHandler, sleep, initial, max)

	if got := int(iter.calls.Load()); got != stopAfter {
		t.Fatalf("iter.Next calls = %d, want %d", got, stopAfter)
	}
	want := []time.Duration{10, 20, 40, 40, 40}
	for i, d := range want {
		want[i] = d * time.Millisecond
	}
	if len(backoffs) != len(want) {
		t.Fatalf("backoffs = %v, want %v", backoffs, want)
	}
	for i := range want {
		if backoffs[i] != want[i] {
			t.Fatalf("backoff[%d] = %v, want %v (full: %v)", i, backoffs[i], want[i], backoffs)
		}
	}
}

// TestConsumeMessagesDoesNotHotLoop uses real sleeps to show that a persistent
// iterator error does not spin the CPU: the number of Next calls stays bounded
// by the backoff interval.
func TestConsumeMessagesDoesNotHotLoop(t *testing.T) {
	closed := &atomic.Bool{}
	iter := &errorIterator{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const window = 120 * time.Millisecond
	const backoff = 20 * time.Millisecond
	time.AfterFunc(window, cancel)
	consumeMessages(ctx, closed, iter, "subject", noopHandler, sleepWithContext, backoff, backoff)

	calls := int(iter.calls.Load())
	// Without backoff this would be thousands of calls in the same window.
	if calls > 20 {
		t.Fatalf("iter.Next called %d times in %v — hot loop not throttled", calls, window)
	}
	if calls < 2 {
		t.Fatalf("iter.Next called only %d time(s) — expected retries with backoff", calls)
	}
}

// TestConsumeMessagesStopsWhenClosed verifies the loop exits after a backoff
// if the bus is closed while it is waiting.
func TestConsumeMessagesStopsWhenClosed(t *testing.T) {
	closed := &atomic.Bool{}
	iter := &errorIterator{}
	ctx := context.Background()

	sleep := func(context.Context, time.Duration) bool {
		closed.Store(true)
		return true
	}
	consumeMessages(ctx, closed, iter, "subject", noopHandler, sleep, time.Millisecond, time.Millisecond)

	if got := int(iter.calls.Load()); got != 1 {
		t.Fatalf("iter.Next calls = %d, want 1 (must stop once closed)", got)
	}
}

func TestNATSRequiresConnection(t *testing.T) {
	_, err := New(context.Background(), eventbus.Config{
		Type: "nats",
		NATS: eventbus.NATSConfig{
			URL: "nats://127.0.0.1:4222",
		},
	})
	// Should fail if no NATS running — that's expected in unit tests
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
}

func TestNATSBusContract(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Type:     "nats",
		Topic:    "loza.test.events",
		DLQTopic: "loza.test.dlq",
		NATS: eventbus.NATSConfig{
			URL:     "nats://127.0.0.1:4222",
			Stream:  "LOZA_TEST",
			Subject: "loza.test.events",
			Durable: "loza-test-worker",
		},
	})
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer bus.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	handler := func(_ context.Context, msg eventbus.Message) error {
		close(done)
		return msg.Ack(ctx)
	}

	if err := bus.Subscribe(ctx, "loza.test.events", "loza-test-worker", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	envelopes := []eventbus.Envelope{
		{ID: "nats-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"x":1}`)},
	}
	if err := bus.Publish(ctx, "loza.test.events", envelopes); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
		// success
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}
}

// blockingIterator models jetstream.MessagesContext.Next with NextContext:
// Next blocks until the context is cancelled (returning ctx.Err) or Stop is
// called (returning ErrMsgIteratorClosed). It also records whether callers
// passed a NextOpt, since an option-less Next cannot be cancelled at all.
type blockingIterator struct {
	ctx     context.Context
	entered chan struct{}
	stopped chan struct{}
	once    sync.Once

	mu       sync.Mutex
	nextOpts int
}

func newBlockingIterator(ctx context.Context) *blockingIterator {
	return &blockingIterator{
		ctx:     ctx,
		entered: make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (i *blockingIterator) Next(opts ...jetstream.NextOpt) (jetstream.Msg, error) {
	i.once.Do(func() { close(i.entered) })
	i.mu.Lock()
	if len(opts) > 0 {
		i.nextOpts++
	}
	i.mu.Unlock()

	select {
	case <-i.ctx.Done():
		return nil, i.ctx.Err()
	case <-i.stopped:
		return nil, jetstream.ErrMsgIteratorClosed
	}
}

func (i *blockingIterator) Stop() {
	select {
	case <-i.stopped:
	default:
		close(i.stopped)
	}
}

func (i *blockingIterator) calledWithContextOption() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.nextOpts > 0
}

// TestConsumeStopsOnContextCancel reproduces MASS-0181: a blocked iter.Next
// must not keep the consumer goroutine alive after the Subscribe context is
// cancelled.
func TestConsumeStopsOnContextCancel(t *testing.T) {
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	iter := newBlockingIterator(subCtx)
	bus := &natsBus{}
	handler := func(context.Context, eventbus.Message) error { return nil }

	done := make(chan struct{})
	go func() {
		bus.consume(subCtx, iter, "loza.test.events", handler)
		close(done)
	}()

	// Wait until the consumer is blocked inside Next.
	select {
	case <-iter.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer never called Next")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer goroutine did not exit after context cancellation")
	}

	if !iter.calledWithContextOption() {
		t.Fatal("Next was called without a context option; a blocked iterator would not be cancelled")
	}
}

// TestCloseCancelsAndStopsSubscriptions verifies that the cancel hooks stored
// in b.subs are actually wired up by Close.
func TestCloseCancelsAndStopsSubscriptions(t *testing.T) {
	cancelCalled := make(chan struct{})
	stopCalled := make(chan struct{})

	bus := &natsBus{
		cancel: func() {},
		subs: []subscription{
			{
				cancel: func() { close(cancelCalled) },
				stop:   func() { close(stopCalled) },
			},
		},
	}

	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-cancelCalled:
	case <-time.After(time.Second):
		t.Fatal("subscription cancel hook was not called by Close")
	}
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("subscription stop hook was not called by Close")
	}
	if len(bus.subs) != 0 {
		t.Fatalf("expected subscriptions to be cleared on Close, got %d", len(bus.subs))
	}
}

// TestCloseCancelsBusContextUsedBySubscriptions guards MASS-0183: busCtx was
// discarded with "_ = busCtx" and Close cancelled a context no consumer used.
// The bus context must be the parent of subscription contexts so that Close
// actually stops consumers through it.
func TestCloseCancelsBusContextUsedBySubscriptions(t *testing.T) {
	busCtx, busCancel := context.WithCancel(context.Background())
	subCtx, subCancel := context.WithCancel(busCtx)
	defer subCancel()

	stopCalled := make(chan struct{})
	bus := &natsBus{
		ctx:    busCtx,
		cancel: busCancel,
		subs: []subscription{
			{cancel: subCancel, stop: func() { close(stopCalled) }},
		},
	}

	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !errors.Is(busCtx.Err(), context.Canceled) {
		t.Fatalf("bus context not cancelled by Close: %v", busCtx.Err())
	}
	if !errors.Is(subCtx.Err(), context.Canceled) {
		t.Fatalf("subscription context derived from bus context not cancelled: %v", subCtx.Err())
	}
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("subscription stop hook was not called by Close")
	}
}
