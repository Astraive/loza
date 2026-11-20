package eventbus

import (
	"testing"

	"github.com/astraive/loza/cortex/internal/metrics"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
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

func droppedCount(subscription string) float64 {
	return testutil.ToFloat64(metrics.EventBusDroppedEvents.WithLabelValues(subscription))
}

// TestPublishFullSubscriberBuffersAreCounted verifies that events lost to a
// full subscriber buffer are reported instead of being silently discarded.
func TestPublishFullSubscriberBuffersAreCounted(t *testing.T) {
	baselineWildcard := droppedCount("wildcard")
	baselineIncident := droppedCount("incident")
	baselineService := droppedCount("service")

	bus := New().WithBufferSize(1)
	wildcard := bus.Subscribe("")
	incident := bus.Subscribe("incident-1")

	// Subscribe only creates wildcard and incident subscriptions; the public
	// API maps a non-empty filter into the incident namespace. Publish still
	// has a distinct service namespace, so register a channel there directly
	// to exercise the service drop path with namespaced keys.
	service := make(chan *models.Event, 1)
	serviceKey := serviceKeyPrefix + "svc-1"
	bus.mu.Lock()
	bus.subscribers[serviceKey] = append(bus.subscribers[serviceKey], service)
	bus.mu.Unlock()

	event := &models.Event{
		ID:         "event-1",
		IncidentID: "incident-1",
		Service:    "svc-1",
	}

	// The first publish fills every one-byte subscriber buffer.
	bus.Publish(event)
	require.Len(t, wildcard, 1)
	require.Len(t, incident, 1)
	require.Len(t, service, 1)
	require.Equal(t, baselineWildcard, droppedCount("wildcard"))
	require.Equal(t, baselineIncident, droppedCount("incident"))
	require.Equal(t, baselineService, droppedCount("service"))

	// The second publish cannot be delivered and must be counted on all three
	// drop paths rather than disappearing without a trace.
	bus.Publish(event)
	require.Len(t, wildcard, 1, "no event may be appended to a full buffer")
	require.Len(t, incident, 1, "no event may be appended to a full buffer")
	require.Len(t, service, 1, "no event may be appended to a full buffer")
	require.Equal(t, baselineWildcard+1, droppedCount("wildcard"))
	require.Equal(t, baselineIncident+1, droppedCount("incident"))
	require.Equal(t, baselineService+1, droppedCount("service"))
}

// TestPublishWithBufferCapacityDoesNotCountDrops ensures the counter is not
// incremented on the normal, successful delivery path.
func TestPublishWithBufferCapacityDoesNotCountDrops(t *testing.T) {
	baseline := droppedCount("wildcard")

	bus := New().WithBufferSize(4)
	ch := bus.Subscribe("")
	bus.Publish(&models.Event{ID: "event-1"})

	require.Len(t, ch, 1)
	require.Equal(t, baseline, droppedCount("wildcard"))
}

// TestUnsubscribeRemovesEmptyFilterKey verifies that removing the last channel
// for a filter deletes the map key instead of leaving an empty slice behind.
func TestUnsubscribeRemovesEmptyFilterKey(t *testing.T) {
	bus := New()

	// Subscriptions live under namespaced keys, never under the raw filter.
	key := incidentKeyPrefix + "incident-1"
	ch := bus.Subscribe("incident-1")
	if _, ok := bus.subscribers[key]; !ok {
		t.Fatal("expected namespaced filter key to exist after Subscribe")
	}
	if _, ok := bus.subscribers["incident-1"]; ok {
		t.Fatal("did not expect an un-namespaced filter key")
	}

	bus.Unsubscribe("incident-1", ch)

	if _, ok := bus.subscribers[key]; ok {
		t.Fatal("expected namespaced filter key to be removed after last Unsubscribe")
	}
	if got := bus.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

// TestUnsubscribeKeepsFilterKeyWithRemainingSubscribers verifies that a filter
// key is retained while other subscribers for that filter are still present.
func TestUnsubscribeKeepsFilterKeyWithRemainingSubscribers(t *testing.T) {
	bus := New()

	key := incidentKeyPrefix + "incident-1"
	first := bus.Subscribe("incident-1")
	second := bus.Subscribe("incident-1")

	bus.Unsubscribe("incident-1", first)

	subs, ok := bus.subscribers[key]
	if !ok {
		t.Fatal("expected namespaced filter key to remain while subscribers exist")
	}
	if len(subs) != 1 {
		t.Fatalf("len(subscribers[%s]) = %d, want 1", key, len(subs))
	}
	if subs[0] != second {
		t.Fatal("expected remaining subscriber to be the second channel")
	}
	if got := bus.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount() = %d, want 1", got)
	}

	bus.Unsubscribe("incident-1", second)
	if _, ok := bus.subscribers[key]; ok {
		t.Fatal("expected namespaced filter key to be removed after last Unsubscribe")
	}
}

// TestUnsubscribeOnlyRemovesEmptiedFilterKey verifies that removing one filter
// does not affect keys for other filters that still have subscribers.
func TestUnsubscribeOnlyRemovesEmptiedFilterKey(t *testing.T) {
	bus := New()

	incidentKey := incidentKeyPrefix + "incident-1"
	wildcardSubKey := wildcardKey
	incidentCh := bus.Subscribe("incident-1")
	wildcardCh := bus.Subscribe("")

	bus.Unsubscribe("incident-1", incidentCh)

	if _, ok := bus.subscribers[incidentKey]; ok {
		t.Fatal("expected emptied namespaced filter key to be removed")
	}
	if _, ok := bus.subscribers[wildcardSubKey]; !ok {
		t.Fatal("expected wildcard filter key to remain")
	}
	if got := bus.SubscriberCount(); got != 1 {
		t.Fatalf("SubscriberCount() = %d, want 1", got)
	}

	bus.Unsubscribe("", wildcardCh)
	if len(bus.subscribers) != 0 {
		t.Fatalf("len(subscribers) = %d, want 0", len(bus.subscribers))
	}
}
