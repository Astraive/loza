package eventbus_test

import (
	"context"
	"testing"
	"time"

	collectorevent "github.com/astraive/loza/collector/internal/event"
	"github.com/astraive/loza/collector/internal/eventbus"
)

// captureBus records envelopes published through it so tests can assert on
// the metadata that reached the bus.
type captureBus struct {
	topic     string
	envelopes []eventbus.Envelope
}

func (b *captureBus) Publish(_ context.Context, topic string, events []eventbus.Envelope) error {
	b.topic = topic
	b.envelopes = append(b.envelopes, events...)
	return nil
}

func (b *captureBus) Subscribe(context.Context, string, string, eventbus.Handler) error { return nil }

func (b *captureBus) Close(context.Context) error { return nil }

func (b *captureBus) Health(context.Context) eventbus.Health { return eventbus.Health{OK: true} }

// TestSinkAdapterWriteEventPopulatesMetadata is a regression test for the
// defect where WriteEvent discarded the typed event and published an
// Envelope with no event/service/id/timestamp metadata.
func TestSinkAdapterWriteEventPopulatesMetadata(t *testing.T) {
	ts := time.Date(2024, 3, 14, 15, 9, 26, 0, time.UTC)
	ev := &collectorevent.Event{
		Timestamp: ts,
		EventID:   "evt-123",
		Event:     "order.created",
		Service:   "checkout",
	}
	encoded := []byte(`{"order":"42"}`)

	bus := &captureBus{}
	sink := eventbus.NewSinkAdapter(bus, "orders.topic")
	if err := sink.WriteEvent(context.Background(), encoded, ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	if bus.topic != "orders.topic" {
		t.Errorf("published topic = %q, want %q", bus.topic, "orders.topic")
	}
	if len(bus.envelopes) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(bus.envelopes))
	}
	got := bus.envelopes[0]
	if got.Event != ev.Event {
		t.Errorf("Event = %q, want %q", got.Event, ev.Event)
	}
	if got.Service != ev.Service {
		t.Errorf("Service = %q, want %q", got.Service, ev.Service)
	}
	if got.ID != ev.EventID {
		t.Errorf("ID = %q, want event ID %q", got.ID, ev.EventID)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
	if string(got.Body) != string(encoded) {
		t.Errorf("Body = %q, want %q", got.Body, encoded)
	}
}

// TestSinkAdapterWriteEventNilEvent keeps the optional *Event argument safe:
// a nil event must not panic and still yields a usable envelope ID/timestamp.
func TestSinkAdapterWriteEventNilEvent(t *testing.T) {
	bus := &captureBus{}
	sink := eventbus.NewSinkAdapter(bus, "topic")
	if err := sink.WriteEvent(context.Background(), []byte(`{}`), nil); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if len(bus.envelopes) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(bus.envelopes))
	}
	got := bus.envelopes[0]
	if got.ID == "" {
		t.Error("ID is empty for nil event")
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp is zero for nil event")
	}
}
