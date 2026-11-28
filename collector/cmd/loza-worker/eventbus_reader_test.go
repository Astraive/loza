package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	collectorevent "github.com/astraive/loza/collector/internal/event"
	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/astraive/loza/collector/internal/eventbus/memory"
	redisadapter "github.com/astraive/loza/collector/internal/eventbus/redis"
	"github.com/astraive/loza/collector/internal/processing"
	goredis "github.com/redis/go-redis/v9"
)

// capturingBus records the handler passed to Subscribe so tests can invoke it
// directly with a caller-controlled context, mirroring how the redis and kafka
// adapters call handlers with their subscription context.
type capturingBus struct {
	handler eventbus.Handler
}

func (b *capturingBus) Publish(context.Context, string, []eventbus.Envelope) error { return nil }

func (b *capturingBus) Subscribe(_ context.Context, _ string, _ string, h eventbus.Handler) error {
	b.handler = h
	return nil
}

func (b *capturingBus) Close(context.Context) error { return nil }

func (b *capturingBus) Health(context.Context) eventbus.Health { return eventbus.Health{OK: true} }

type fakeMessage struct {
	env   eventbus.Envelope
	acked bool
}

func (m *fakeMessage) ID() string                  { return m.env.ID }
func (m *fakeMessage) Topic() string               { return "topic" }
func (m *fakeMessage) Envelope() eventbus.Envelope { return m.env }
func (m *fakeMessage) Ack(context.Context) error {
	m.acked = true
	return nil
}
func (m *fakeMessage) Nack(context.Context, error) error { return nil }

// registerHandler drives Poll once to install the subscription handler. A very
// short timeout makes Poll return on its deadline without producing records.
func registerHandler(t *testing.T, r *eventbusQueueReader, bus *capturingBus) eventbus.Handler {
	t.Helper()
	if _, err := r.Poll(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("initial Poll: %v", err)
	}
	if bus.handler == nil {
		t.Fatal("subscription handler was not registered")
	}
	return bus.handler
}

func TestEventbusHandlerSendsWhenBufferHasRoom(t *testing.T) {
	bus := &capturingBus{}
	r := newEventbusQueueReader(bus, "topic", "group")
	handler := registerHandler(t, r, bus)

	body := []byte(`{"event":"checkout"}`)
	msg := &fakeMessage{env: eventbus.Envelope{ID: "m1", Body: body}}
	done := make(chan error, 1)
	go func() {
		done <- handler(context.Background(), msg)
	}()

	select {
	case rec := <-r.ch:
		body[0] = 'x'
		if !bytes.Equal(rec.value, []byte(`{"event":"checkout"}`)) {
			t.Fatalf("record value = %q, want original event body", rec.value)
		}
		select {
		case err := <-done:
			t.Fatalf("handler returned before worker commit: %v", err)
		default:
		}
		if err := rec.commit(context.Background()); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if !msg.acked {
			t.Fatal("record commit did not acknowledge the bus message")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("handler returned an error after commit: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("handler did not return after worker commit")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not enqueue a record")
	}
}

type payloadCaptureSink struct {
	payload []byte
}

func (*payloadCaptureSink) Name() string { return "payload_capture" }

func (s *payloadCaptureSink) WriteEvent(_ context.Context, payload []byte, _ *collectorevent.Event) error {
	s.payload = append(s.payload[:0], payload...)
	return nil
}

func (*payloadCaptureSink) Flush(context.Context) error { return nil }
func (*payloadCaptureSink) Close(context.Context) error { return nil }

func TestWorkerEventBusPreservesOriginalPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	bus, err := memory.New(ctx, eventbus.Config{})
	if err != nil {
		t.Fatalf("new memory bus: %v", err)
	}
	reader := newEventbusQueueReader(bus, "events", "worker")
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close reader: %v", err)
		}
	}()
	if records, err := reader.Poll(ctx, 200*time.Millisecond); err != nil {
		t.Fatalf("subscribe reader: %v", err)
	} else if len(records) != 0 {
		t.Fatalf("records before publish = %d, want 0", len(records))
	}

	original := []byte(`{"event_id":"evt-1","event":"checkout","service":"shop","timestamp":"2026-01-01T00:00:00Z","duration_ms":12,"attrs":{"amount":42}}`)
	err = bus.Publish(ctx, "events", []eventbus.Envelope{{
		ID:        "evt-1",
		Event:     "checkout",
		Service:   "shop",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Body:      original,
	}})
	if err != nil {
		t.Fatalf("publish event: %v", err)
	}
	records, err := reader.Poll(ctx, time.Second)
	if err != nil {
		t.Fatalf("poll event: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	sink := &payloadCaptureSink{}
	processor, err := processing.New(processing.Config{}, sink, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() {
		if err := processor.Close(); err != nil {
			t.Errorf("close processor: %v", err)
		}
	}()
	state := &workerState{processor: processor}
	state.handleRecord(ctx, records[0])

	var want, got map[string]any
	if err := json.Unmarshal(original, &want); err != nil {
		t.Fatalf("unmarshal original event: %v", err)
	}
	if err := json.Unmarshal(sink.payload, &got); err != nil {
		t.Fatalf("unmarshal sink payload %q: %v", sink.payload, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sink payload = %s, want original event %s", sink.payload, original)
	}
}

func TestRedisWorkerAcknowledgmentWaitsForCommit(t *testing.T) {
	const (
		stream = "loza.worker.ack"
		group  = "loza-worker-ack-test"
	)
	mr := miniredis.RunT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bus, err := redisadapter.New(ctx, eventbus.Config{
		Type:          "redis",
		Topic:         stream,
		ConsumerGroup: group,
		Redis: eventbus.RedisConfig{
			Addr:   mr.Addr(),
			Stream: stream,
			Group:  group,
		},
	})
	if err != nil {
		t.Fatalf("new Redis bus: %v", err)
	}
	reader := newEventbusQueueReader(bus, stream, group)
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close reader: %v", err)
		}
	}()
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	}()

	if records, err := reader.Poll(ctx, 50*time.Millisecond); err != nil {
		t.Fatalf("subscribe reader: %v", err)
	} else if len(records) != 0 {
		t.Fatalf("records before publish = %d, want 0", len(records))
	}
	err = bus.Publish(ctx, stream, []eventbus.Envelope{{
		ID:        "worker-ack-1",
		Event:     "checkout",
		Timestamp: time.Now().UTC(),
		Body:      []byte(`{"event":"checkout"}`),
	}})
	if err != nil {
		t.Fatalf("publish event: %v", err)
	}
	records, err := reader.Poll(ctx, time.Second)
	if err != nil {
		t.Fatalf("poll event: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	pending, err := client.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("pending before worker commit: %v", err)
	}
	if pending.Count != 1 {
		t.Fatalf("pending before worker commit = %d, want 1", pending.Count)
	}
	if err := records[0].commit(ctx); err != nil {
		t.Fatalf("worker commit: %v", err)
	}
	pending, err = client.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("pending after worker commit: %v", err)
	}
	if pending.Count != 0 {
		t.Fatalf("pending after worker commit = %d, want 0", pending.Count)
	}
}

func TestEventbusHandlerUnblocksOnReaderCloseBeforeCommit(t *testing.T) {
	bus := &capturingBus{}
	r := newEventbusQueueReader(bus, "topic", "group")
	handler := registerHandler(t, r, bus)

	done := make(chan error, 1)
	go func() {
		done <- handler(context.Background(), &fakeMessage{env: eventbus.Envelope{ID: "m1"}})
	}()
	select {
	case <-r.ch:
	case <-time.After(time.Second):
		t.Fatal("handler did not enqueue a record")
	}
	select {
	case err := <-done:
		t.Fatalf("handler returned before commit: %v", err)
	default:
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not unblock after reader close")
	}
}

func TestEventbusHandlerUnblocksOnCancelWhenBufferFull(t *testing.T) {
	bus := &capturingBus{}
	r := newEventbusQueueReader(bus, "topic", "group")
	handler := registerHandler(t, r, bus)

	// Fill the fixed-size buffer so the handler's send cannot complete.
	for i := 0; i < cap(r.ch); i++ {
		r.ch <- queueRecord{}
	}

	hCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- handler(hCtx, &fakeMessage{env: eventbus.Envelope{ID: "m2"}})
	}()

	// With an unconditional send this goroutine blocks here forever.
	select {
	case err := <-done:
		t.Fatalf("handler returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Cancellation (e.g. from Close) must unblock the handler and surface an
	// error so the adapter can Nack / redeliver the message.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not unblock after context cancellation")
	}
}
