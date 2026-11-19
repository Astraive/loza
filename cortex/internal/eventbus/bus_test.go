package eventbus

import (
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

// bufferedEvents drains a subscription channel without blocking. Publish
// delivers synchronously, so all matching events are already buffered once
// Publish returns.
func bufferedEvents(ch <-chan *models.Event) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// TestWithBufferSizeControlsSubscriberCapacity verifies that WithBufferSize is
// not dead code: it must determine the capacity of newly created subscriber
// channels. Publish drops events once a subscriber is full, so the number of
// buffered events after publishing more than the capacity equals that capacity.
func TestWithBufferSizeControlsSubscriberCapacity(t *testing.T) {
	bus := New().WithBufferSize(3)
	ch := bus.Subscribe("")
	defer bus.Unsubscribe("", ch)

	for i := 0; i < 10; i++ {
		bus.Publish(&models.Event{})
	}

	if got := len(ch); got != 3 {
		t.Fatalf("configured buffer size: expected 3 buffered events, got %d", got)
	}
}

// TestNewUsesDefaultBufferSize verifies the unchanged default when no
// configured size is supplied.
func TestNewUsesDefaultBufferSize(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("")
	defer bus.Unsubscribe("", ch)

	for i := 0; i < 200; i++ {
		bus.Publish(&models.Event{})
	}

	if got := len(ch); got != 128 {
		t.Fatalf("default buffer size: expected 128 buffered events, got %d", got)
	}
}

func TestPublishIncidentFilterIgnoresMatchingService(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("X")
	defer bus.Unsubscribe("X", ch)

	// No incident id: the event must not reach an incident-filtered subscriber
	// merely because its Service equals the filter value.
	bus.Publish(&models.Event{ID: "e1", Service: "X"})

	if got := bufferedEvents(ch); got != 0 {
		t.Fatalf("incident-filtered subscriber received %d event(s) from a service-only match; want 0", got)
	}
}

func TestPublishNoDoubleDeliveryWhenIncidentEqualsService(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("X")
	defer bus.Unsubscribe("X", ch)

	bus.Publish(&models.Event{ID: "e1", IncidentID: "X", Service: "X"})

	if got := bufferedEvents(ch); got != 1 {
		t.Fatalf("received %d event(s); want exactly 1 (no duplicate delivery)", got)
	}
}

func TestPublishIncidentFilterReceivesIncidentEvent(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("incident-A")
	defer bus.Unsubscribe("incident-A", ch)

	bus.Publish(&models.Event{ID: "e1", IncidentID: "incident-A", Service: "other-service"})

	if got := bufferedEvents(ch); got != 1 {
		t.Fatalf("received %d event(s); want 1", got)
	}
}

func TestPublishIncidentFiltersAreIsolated(t *testing.T) {
	bus := New()
	chA := bus.Subscribe("incident-A")
	chB := bus.Subscribe("incident-B")
	defer bus.Unsubscribe("incident-A", chA)
	defer bus.Unsubscribe("incident-B", chB)

	bus.Publish(&models.Event{ID: "e1", IncidentID: "incident-A", Service: "incident-B"})

	if got := bufferedEvents(chA); got != 1 {
		t.Fatalf("incident-A subscriber received %d event(s); want 1", got)
	}
	if got := bufferedEvents(chB); got != 0 {
		t.Fatalf("incident-B subscriber received %d event(s) from a service match; want 0", got)
	}
}

func TestPublishWildcardReceivesAllEvents(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("")
	defer bus.Unsubscribe("", ch)

	bus.Publish(&models.Event{ID: "e1", IncidentID: "inc-1", Service: "svc-1"})

	if got := bufferedEvents(ch); got != 1 {
		t.Fatalf("wildcard subscriber received %d event(s); want 1", got)
	}
}

func TestUnsubscribeRemovesOnlyMatchingSubscription(t *testing.T) {
	bus := New()
	chA := bus.Subscribe("incident-A")
	chB := bus.Subscribe("incident-A")

	bus.Unsubscribe("incident-A", chA)

	if _, open := <-chA; open {
		t.Fatal("unsubscribed channel should be closed")
	}
	if got := bus.SubscriberCount(); got != 1 {
		t.Fatalf("subscriber count = %d; want 1", got)
	}

	bus.Publish(&models.Event{ID: "e1", IncidentID: "incident-A"})
	if got := bufferedEvents(chB); got != 1 {
		t.Fatalf("remaining subscriber received %d event(s); want 1", got)
	}
	bus.Unsubscribe("incident-A", chB)
}
