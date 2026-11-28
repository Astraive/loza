package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/astraive/loza/collector/internal/eventbus"
	goredis "github.com/redis/go-redis/v9"
)

// redisErrorStub mimics a go-redis server reply so we can exercise wrapped
// errors without a live Redis instance.
type redisErrorStub string

func (e redisErrorStub) Error() string { return string(e) }
func (e redisErrorStub) RedisError()   {}

// TestIsBusyGroupErr is the regression test for the BUSYGROUP detection used
// by both New and Subscribe. The wrapped-reply cases fail with the old exact
// string comparison that New used (and that isBusyGroupErr inherited).
func TestIsBusyGroupErr(t *testing.T) {
	const exact = "BUSYGROUP Consumer Group name already exists"

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"exact message", errors.New(exact), true},
		{"prefix variant", errors.New(exact + " (driver variant)"), true},
		{"wrapped redis reply", fmt.Errorf("create group: %w", redisErrorStub(exact)), true},
		{"ERR prefixed reply", redisErrorStub("ERR " + exact), true},
		{"unrelated error", errors.New("WRONGTYPE Operation against a key"), false},
		{"other BUSY code", redisErrorStub("BUSYKEY Target key name already exists"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBusyGroupErr(tt.err); got != tt.want {
				t.Fatalf("isBusyGroupErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestNewPreexistingGroup verifies the real New code path: starting with a
// group that already exists must not fail. This exercises the New call site
// that previously used a brittle exact-string comparison.
func TestNewPreexistingGroup(t *testing.T) {
	m := miniredis.RunT(t)
	defer m.Close()

	ctx := context.Background()
	const stream = "loza.test.events"
	const group = "loza-test-group"

	setup := goredis.NewClient(&goredis.Options{Addr: m.Addr()})
	defer setup.Close()
	if err := setup.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		t.Fatalf("pre-create group: %v", err)
	}

	bus, err := New(ctx, eventbus.Config{
		Type:          "redis",
		Topic:         stream,
		ConsumerGroup: group,
		Redis: eventbus.RedisConfig{
			Addr:   m.Addr(),
			Stream: stream,
			Group:  group,
		},
	})
	if err != nil {
		t.Fatalf("New with pre-existing group: %v", err)
	}
	defer bus.Close(ctx)
}

func TestRedisRequiresConnection(t *testing.T) {
	_, err := New(context.Background(), eventbus.Config{
		Type: "redis",
		Redis: eventbus.RedisConfig{
			Addr: "127.0.0.1:6379",
		},
	})
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
}

func TestRedisBusContract(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Type:          "redis",
		Topic:         "loza.test.events",
		ConsumerGroup: "loza-test-group",
		Redis: eventbus.RedisConfig{
			Addr:   "127.0.0.1:6379",
			Stream: "loza.test.events",
			Group:  "loza-test-group",
		},
	})
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer bus.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	handler := func(_ context.Context, msg eventbus.Message) error {
		close(done)
		return msg.Ack(ctx)
	}

	if err := bus.Subscribe(ctx, "loza.test.events", "loza-test-group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	envelopes := []eventbus.Envelope{
		{ID: "redis-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"x":1}`)},
	}
	if err := bus.Publish(ctx, "loza.test.events", envelopes); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}
}

// newTestBus creates a Redis bus backed by an in-process miniredis server.
func newTestBus(t *testing.T, stream, group, dlq string) (*redisBus, context.CancelFunc) {
	t.Helper()
	mr := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(context.Background())
	bus, err := New(ctx, eventbus.Config{
		Type:          "redis",
		Topic:         stream,
		ConsumerGroup: group,
		DLQTopic:      dlq,
		Redis: eventbus.RedisConfig{
			Addr:   mr.Addr(),
			Stream: stream,
			Group:  group,
		},
	})
	if err != nil {
		cancel()
		t.Fatalf("new bus: %v", err)
	}
	return bus.(*redisBus), cancel
}

// TestRedisFailedHandlerIsRedelivered reproduces MASS-0185: an error returned
// by the handler used to leave the message stranded in the PEL forever because
// consume only ever reads new entries with ">". The message must now be
// requeued and delivered again until it eventually succeeds.
func TestRedisFailedHandlerIsRedelivered(t *testing.T) {
	bus, cancel := newTestBus(t, "loza.test.retry", "loza-test-retry-group", "")
	defer cancel()
	defer bus.Close(context.Background())

	ctx, timeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer timeout()

	var calls atomic.Int32
	var once sync.Once
	done := make(chan struct{})
	handler := func(context.Context, eventbus.Message) error {
		if calls.Add(1) < maxRedisDeliveryAttempts {
			return errors.New("transient handler failure")
		}
		once.Do(func() { close(done) })
		return nil
	}
	if err := bus.Subscribe(ctx, "loza.test.retry", "loza-test-retry-group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	env := eventbus.Envelope{ID: "redis-retry-1", Event: "test.retry", Timestamp: time.Now(), Body: []byte(`{"x":1}`)}
	if err := bus.Publish(ctx, "loza.test.retry", []eventbus.Envelope{env}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("message was not redelivered to the handler (calls=%d)", calls.Load())
	}
	if got := calls.Load(); got < maxRedisDeliveryAttempts {
		t.Fatalf("handler called %d times, want at least %d (no redelivery occurred)", got, maxRedisDeliveryAttempts)
	}
}

// TestRedisFailedHandlerIsDeadLettered verifies that a permanently failing
// message is moved to the DLQ after the retry budget is exhausted, and that
// the original pending entry is acknowledged rather than left in the PEL.
func TestRedisFailedHandlerIsDeadLettered(t *testing.T) {
	const (
		stream = "loza.test.dlq"
		group  = "loza-test-dlq-group"
		dlq    = "loza.test.dlq.sink"
	)
	bus, cancel := newTestBus(t, stream, group, dlq)
	defer cancel()
	defer bus.Close(context.Background())

	ctx, timeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer timeout()

	handlerErr := errors.New("permanent handler failure")
	if err := bus.Subscribe(ctx, stream, group, func(context.Context, eventbus.Message) error {
		return handlerErr
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	env := eventbus.Envelope{ID: "redis-dlq-1", Event: "test.dlq", Timestamp: time.Now(), Body: []byte(`{"x":1}`)}
	if err := bus.Publish(ctx, stream, []eventbus.Envelope{env}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var dlqMsgs []goredis.XMessage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := bus.client.XRange(ctx, dlq, "-", "+").Result()
		if err != nil {
			t.Fatalf("read dlq: %v", err)
		}
		if len(msgs) > 0 {
			dlqMsgs = msgs
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(dlqMsgs) == 0 {
		t.Fatal("failing message was never moved to the DLQ")
	}

	body, ok := dlqMsgs[0].Values["body"].(string)
	if !ok {
		t.Fatalf("DLQ message has no body: %#v", dlqMsgs[0].Values)
	}
	var got eventbus.Envelope
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal dlq body: %v", err)
	}
	if got.ID != env.ID {
		t.Errorf("DLQ envelope ID = %q, want %q", got.ID, env.ID)
	}
	if got.Attempts < maxRedisDeliveryAttempts {
		t.Errorf("DLQ envelope attempts = %d, want >= %d", got.Attempts, maxRedisDeliveryAttempts)
	}
	if reason := got.Headers["dlq_reason"]; reason != handlerErr.Error() {
		t.Errorf("DLQ reason = %q, want %q", reason, handlerErr.Error())
	}

	pending, err := bus.client.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("xpending: %v", err)
	}
	if pending.Count != 0 {
		t.Errorf("original stream has %d pending entries, want 0", pending.Count)
	}
}

// TestRedisMessageNackRequeues verifies that Nack actually releases the entry
// so the message is redelivered, instead of being a no-op.
func TestRedisMessageNackRequeues(t *testing.T) {
	const (
		stream = "loza.test.nack"
		group  = "loza-test-nack-group"
	)
	bus, cancel := newTestBus(t, stream, group, "")
	defer cancel()
	defer bus.Close(context.Background())

	ctx, timeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer timeout()

	var calls atomic.Int32
	var once sync.Once
	done := make(chan struct{})
	handler := func(hCtx context.Context, msg eventbus.Message) error {
		if calls.Add(1) == 1 {
			return msg.Nack(hCtx, errors.New("explicit nack"))
		}
		once.Do(func() { close(done) })
		return msg.Ack(hCtx)
	}
	if err := bus.Subscribe(ctx, stream, group, handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	env := eventbus.Envelope{ID: "redis-nack-1", Event: "test.nack", Timestamp: time.Now(), Body: []byte(`{"x":1}`)}
	if err := bus.Publish(ctx, stream, []eventbus.Envelope{env}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("nacked message was not redelivered (calls=%d)", calls.Load())
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("handler called %d times, want at least 2 after Nack", got)
	}
}

func TestRedisActiveMessageClaimHeartbeatPreventsReclaim(t *testing.T) {
	const (
		stream = "loza.test.active-claim"
		group  = "loza-test-active-claim-group"
	)
	server := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	newBus := func() *redisBus {
		bus, err := New(ctx, eventbus.Config{
			Type:          "redis",
			Topic:         stream,
			ConsumerGroup: group,
			Redis: eventbus.RedisConfig{
				Addr:   server.Addr(),
				Stream: stream,
				Group:  group,
			},
		})
		if err != nil {
			t.Fatalf("new bus: %v", err)
		}
		return bus.(*redisBus)
	}
	first, second := newBus(), newBus()
	defer first.Close(context.Background())
	defer second.Close(context.Background())
	for _, bus := range []*redisBus{first, second} {
		bus.pendingClaimIdle = 100 * time.Millisecond
		bus.pendingClaimInterval = 10 * time.Millisecond
		bus.claimHeartbeatInterval = 10 * time.Millisecond
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	handlerReturned := make(chan struct{}, 1)
	handler := func(handlerCtx context.Context, msg eventbus.Message) error {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			handlerReturned <- struct{}{}
			return nil
		case <-handlerCtx.Done():
			return handlerCtx.Err()
		}
	}
	if err := first.Subscribe(ctx, stream, group, handler); err != nil {
		t.Fatalf("subscribe first consumer: %v", err)
	}
	env := eventbus.Envelope{
		ID: "redis-active-claim-1", Event: "test.active-claim",
		Timestamp: time.Now(), Body: []byte(`{"x":1}`),
	}
	if err := first.Publish(ctx, stream, []eventbus.Envelope{env}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first consumer did not start handling the message")
	}

	secondHandled := make(chan struct{}, 1)
	if err := second.Subscribe(ctx, stream, group, func(context.Context, eventbus.Message) error {
		secondHandled <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("subscribe second consumer: %v", err)
	}
	select {
	case <-secondHandled:
		t.Fatal("second consumer reclaimed a message with an active handler")
	case <-time.After(redisReadBlock + 250*time.Millisecond):
	}

	close(release)
	select {
	case <-handlerReturned:
	case <-time.After(time.Second):
		t.Fatal("first consumer did not finish after release")
	}
}
