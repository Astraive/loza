package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
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
	env eventbus.Envelope
}

func (m *fakeMessage) ID() string                        { return m.env.ID }
func (m *fakeMessage) Topic() string                     { return "topic" }
func (m *fakeMessage) Envelope() eventbus.Envelope       { return m.env }
func (m *fakeMessage) Ack(context.Context) error         { return nil }
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

	msg := &fakeMessage{env: eventbus.Envelope{ID: "m1"}}
	if err := handler(context.Background(), msg); err != nil {
		t.Fatalf("handler returned error with room in buffer: %v", err)
	}

	select {
	case rec := <-r.ch:
		if string(rec.value) == "" {
			t.Fatal("handler produced an empty record")
		}
	default:
		t.Fatal("handler did not enqueue a record")
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
