package eventbus

import (
	"sync"

	"github.com/astraive/loza/cortex/internal/metrics"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	// wildcardKey holds subscribers that receive every published event.
	wildcardKey = "*"
	// incidentKeyPrefix namespaces subscriptions to a specific incident id.
	incidentKeyPrefix = "incident:"
	// serviceKeyPrefix namespaces subscriptions to a specific service name.
	serviceKeyPrefix = "service:"
)

// subscriptionKey maps a Subscribe/Unsubscribe filter to its namespaced key.
// An empty filter subscribes to all events; any other filter is an incident id.
// Keeping incident and service keys in separate namespaces prevents an
// incident id and a service name with the same value from colliding (or being
// delivered twice to the same channel).
func subscriptionKey(filter string) string {
	if filter == "" {
		return wildcardKey
	}
	return incidentKeyPrefix + filter
}
// dropLog samples drop warnings so a slow subscriber cannot flood the logs;
// every drop is still counted in metrics.EventBusDroppedEvents.
var dropLog = log.Logger.Sample(&zerolog.BasicSampler{N: 100})

// EventBus provides in-memory pub/sub for events.
// Used by the processor to publish ingested events and by
// gRPC StreamEvents to subscribe.
type EventBus struct {
	subscribers map[string][]chan *models.Event
	mu          sync.RWMutex
	bufferSize  int
}

// New creates a new EventBus with default buffer size (128).
func New() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]chan *models.Event),
		bufferSize:  128,
	}
}

// WithBufferSize sets the subscriber channel buffer size.
func (b *EventBus) WithBufferSize(size int) *EventBus {
	if size > 0 {
		b.bufferSize = size
	}
	return b
}

// Subscribe creates a new subscription with an optional filter.
// filter="" means all events; a non-empty filter is matched against an
// event's incident id. Returns a channel that receives events.
func (b *EventBus) Subscribe(filter string) <-chan *models.Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := subscriptionKey(filter)
	ch := make(chan *models.Event, b.bufferSize)
	b.subscribers[key] = append(b.subscribers[key], ch)

	log.Info().Str("filter", filter).Msg("EventBus: new subscriber")
	return ch
}

// Unsubscribe removes a subscription and closes its channel.
func (b *EventBus) Unsubscribe(filter string, ch <-chan *models.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := subscriptionKey(filter)
	subs := b.subscribers[key]
	for i, sub := range subs {
		// Compare the underlying channel
		if sub == ch {
			b.subscribers[key] = append(subs[:i], subs[i+1:]...)
			remaining := append(subs[:i], subs[i+1:]...)
			if len(remaining) == 0 {
				// Drop the key so empty subscriptions are not left in the map.
				delete(b.subscribers, filter)
			} else {
				b.subscribers[filter] = remaining
			}
			close(sub)
			break
		}
	}
}

// Publish sends an event to all matching subscribers.
func (b *EventBus) Publish(event *models.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Each category uses its own key namespace, so an incident id and a
	// service name that happen to be equal can neither collide nor cause the
	// same subscription to be delivered the event twice.
	for _, ch := range b.subscribers[wildcardKey] {
		deliver(ch, event)
	// Send to wildcard subscribers
	for _, ch := range b.subscribers[""] {
		b.deliver(ch, event, "wildcard", "")
	}

	if event.IncidentID != "" {
		for _, ch := range b.subscribers[incidentKeyPrefix+event.IncidentID] {
			deliver(ch, event)
		for _, ch := range b.subscribers[event.IncidentID] {
			b.deliver(ch, event, "incident", event.IncidentID)
		}
	}

	if event.Service != "" {
		for _, ch := range b.subscribers[serviceKeyPrefix+event.Service] {
			deliver(ch, event)
		}
	}
}

// deliver performs a non-blocking send, dropping the event if the
// subscriber's buffer is full.
func deliver(ch chan *models.Event, event *models.Event) {
	select {
	case ch <- event:
	default:
		for _, ch := range b.subscribers[event.Service] {
			b.deliver(ch, event, "service", event.Service)
		}
	}
}

// deliver performs the non-blocking send to a subscriber. A full buffer is no
// longer a silent failure: it increments a counter and emits a sampled warning
// so operators can detect and attribute lost events.
func (b *EventBus) deliver(ch chan *models.Event, event *models.Event, subscription, filter string) {
	select {
	case ch <- event:
	default:
		metrics.EventBusDroppedEvents.WithLabelValues(subscription).Inc()
		dropLog.Warn().
			Str("subscription", subscription).
			Str("filter", filter).
			Str("event_id", event.ID).
			Msg("EventBus: subscriber buffer full, dropping event")
	}
}

// SubscriberCount returns the number of active subscribers.
func (b *EventBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	count := 0
	for _, subs := range b.subscribers {
		count += len(subs)
	}
	return count
}
