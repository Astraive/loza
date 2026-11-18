package memory

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
)

func TestBusContract(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Type:   "memory",
		Topic:  "test",
		Memory: eventbus.MemoryConfig{BufferSize: 100},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	var received atomic.Int32
	handler := func(_ context.Context, msg eventbus.Message) error {
		received.Add(1)
		return msg.Ack(context.Background())
	}

	ctx := context.Background()
	if err := bus.Subscribe(ctx, "test-topic", "group-1", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	envelopes := []eventbus.Envelope{
		{ID: "1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"a":1}`)},
		{ID: "2", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"a":2}`)},
		{ID: "3", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"a":3}`)},
	}

	if err := bus.Publish(ctx, "test-topic", envelopes); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if got := received.Load(); got != 3 {
		t.Fatalf("expected 3 messages, got %d", got)
	}
}

func TestPublishNoSubscribers(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	if err := bus.Publish(context.Background(), "no-topic", []eventbus.Envelope{
		{ID: "1", Event: "test", Timestamp: time.Now(), Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("publish to empty topic should not error: %v", err)
	}
}

func TestHealth(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}

	h := bus.Health(context.Background())
	if !h.OK {
		t.Fatal("expected healthy bus")
	}

	bus.Close(context.Background())
	h = bus.Health(context.Background())
	if h.OK {
		t.Fatal("expected unhealthy after close")
	}
}

func TestDLQ(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		DLQTopic: "dlq",
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	memBus := bus.(*Bus)
	env := eventbus.Envelope{ID: "dlq-1", Event: "failed", Timestamp: time.Now(), Body: []byte(`{}`), Headers: map[string]string{}}
	if err := memBus.PublishDLQ(context.Background(), env, fmt.Errorf("test error")); err != nil {
		t.Fatalf("publish dlq: %v", err)
	}

	msgs := memBus.DLQMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 DLQ message, got %d", len(msgs))
	}
	if msgs[0].ID != "dlq-1" {
		t.Fatalf("expected DLQ message ID dlq-1, got %s", msgs[0].ID)
	}
}

func TestPublishAfterClose(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	bus.Close(context.Background())

	err = bus.Publish(context.Background(), "t", []eventbus.Envelope{{ID: "x"}})
	if err != eventbus.ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

func TestSubscribeAfterClose(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	bus.Close(context.Background())

	handler := func(_ context.Context, _ eventbus.Message) error { return nil }
	err = bus.Subscribe(context.Background(), "t", "g", handler)
	if err != eventbus.ErrBusClosed {
		t.Errorf("expected ErrBusClosed, got %v", err)
	}
}

// waitForDLQ polls the in-process DLQ until it holds want messages.
func waitForDLQ(t *testing.T, bus *Bus, want int) []eventbus.Envelope {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := bus.DLQMessages(); len(msgs) >= want {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d DLQ messages, got %d", want, len(bus.DLQMessages()))
	return nil
}

// TestNackTriggersRetryThenDLQ reproduces the reported defect: Nack used to
// be an unconditional no-op, so a nacked message was silently treated as a
// success. It must now be retried and eventually dead-lettered.
func TestNackTriggersRetryThenDLQ(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Memory: eventbus.MemoryConfig{BufferSize: 10, MaxRetries: 2},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	ctx := context.Background()
	reason := errors.New("consumer rejected event")
	var calls atomic.Int32
	handler := func(_ context.Context, msg eventbus.Message) error {
		calls.Add(1)
		return msg.Nack(ctx, reason)
	}
	if err := bus.Subscribe(ctx, "nack-topic", "group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "nack-topic", []eventbus.Envelope{
		{ID: "nack-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msgs := waitForDLQ(t, bus.(*Bus), 1)
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 delivery attempts (initial + 2 retries), got %d", got)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 DLQ message, got %d", len(msgs))
	}
	if got := msgs[0].Headers["dlq_reason"]; got != reason.Error() {
		t.Errorf("expected dlq_reason %q, got %q", reason.Error(), got)
	}
	if msgs[0].Attempts != 3 {
		t.Errorf("expected 3 recorded attempts, got %d", msgs[0].Attempts)
	}
}

// TestHandlerErrorRetries covers the equivalent failure path where the
// handler reports failure via its return error instead of calling Nack.
func TestHandlerErrorRetries(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Memory: eventbus.MemoryConfig{BufferSize: 10, MaxRetries: 1},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	ctx := context.Background()
	reason := errors.New("handler failed")
	var calls atomic.Int32
	handler := func(_ context.Context, _ eventbus.Message) error {
		calls.Add(1)
		return reason
	}
	if err := bus.Subscribe(ctx, "err-topic", "group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "err-topic", []eventbus.Envelope{
		{ID: "err-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msgs := waitForDLQ(t, bus.(*Bus), 1)
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 delivery attempts, got %d", got)
	}
	if got := msgs[0].Headers["dlq_reason"]; got != reason.Error() {
		t.Errorf("expected dlq_reason %q, got %q", reason.Error(), got)
	}
}

// TestNackWithoutReasonDeadLetters checks that a bare Nack (no error) is not
// mistaken for success and records a meaningful DLQ reason.
func TestNackWithoutReasonDeadLetters(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Memory: eventbus.MemoryConfig{BufferSize: 10},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	ctx := context.Background()
	handler := func(_ context.Context, msg eventbus.Message) error {
		if err := msg.Ack(ctx); err != nil {
			return err
		}
		// Nack takes precedence over the earlier Ack.
		return msg.Nack(ctx, nil)
	}
	if err := bus.Subscribe(ctx, "bare-nack-topic", "group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "bare-nack-topic", []eventbus.Envelope{
		{ID: "bare-nack-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	msgs := waitForDLQ(t, bus.(*Bus), 1)
	if got := msgs[0].Headers["dlq_reason"]; got != errNack.Error() {
		t.Errorf("expected dlq_reason %q, got %q", errNack.Error(), got)
	}
}

// TestAckPreventsRetry verifies that an explicitly acked, successful handler
// is not redelivered, even when a retry budget is configured.
func TestAckPreventsRetry(t *testing.T) {
	bus, err := New(context.Background(), eventbus.Config{
		Memory: eventbus.MemoryConfig{BufferSize: 10, MaxRetries: 2},
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}
	defer bus.Close(context.Background())

	ctx := context.Background()
	var calls atomic.Int32
	handler := func(_ context.Context, msg eventbus.Message) error {
		calls.Add(1)
		return msg.Ack(ctx)
	}
	if err := bus.Subscribe(ctx, "ack-topic", "group", handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "ack-topic", []eventbus.Envelope{
		{ID: "ack-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Give any accidental redelivery time to surface.
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 delivery attempt for an acked message, got %d", got)
	}
	if got := len(bus.(*Bus).DLQMessages()); got != 0 {
		t.Fatalf("acked message must not reach the DLQ, got %d messages", got)
	}
}
