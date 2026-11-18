package redis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/astraive/loza/collector/internal/eventbus"
	goredis "github.com/redis/go-redis/v9"
)

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
