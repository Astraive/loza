// Package memory implements an in-process event bus using buffered channels.
// Suitable for local dev, testing, and single-process deployments.
//
// Delivery is at-least-once within the process: a handler signals failure by
// returning an error or by calling Message.Nack. A failed delivery is retried
// up to Config.Memory.MaxRetries times before the envelope is dead-lettered.
// A handler that returns nil without nacking is treated as an implicit ack.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/astraive/loza/collector/internal/eventbus"
)

// errNack is the failure reason recorded when a handler nacks a message
// without supplying an explicit error.
var errNack = errors.New("memory bus: message nacked by handler")

func init() {
	eventbus.Register("memory", New)
}

// Bus implements eventbus.Bus using in-process buffered channels.
type Bus struct {
	mu     sync.RWMutex
	topics map[string]*topic
	dlq    []eventbus.Envelope
	dlqMu  sync.Mutex
	closed bool
	cfg    eventbus.Config
}

type topic struct {
	name     string
	ch       chan eventbus.Envelope
	mu       sync.RWMutex
	handlers []handlerEntry
	started  bool
}

type handlerEntry struct {
	group   string
	handler eventbus.Handler
}

// Message implements eventbus.Message for the memory adapter. It records the
// handler's acknowledgment decision so consumeLoop can act on it.
type Message struct {
	id       string
	topic    string
	envelope eventbus.Envelope

	mu      sync.Mutex
	acked   bool
	nacked  bool
	nackErr error
}

func (m *Message) ID() string                  { return m.id }
func (m *Message) Topic() string               { return m.topic }
func (m *Message) Envelope() eventbus.Envelope { return m.envelope }

// Ack marks the message as successfully processed. There is no remote broker
// to confirm to, so this only records the decision for consumeLoop.
func (m *Message) Ack(_ context.Context) error {
	m.mu.Lock()
	m.acked = true
	m.mu.Unlock()
	return nil
}

// Nack marks the message as failed and records the reason so consumeLoop can
// retry or dead-letter it. A nack takes precedence over a prior ack.
func (m *Message) Nack(_ context.Context, err error) error {
	m.mu.Lock()
	m.nacked = true
	m.nackErr = err
	m.mu.Unlock()
	return nil
}

// decision reports whether the handler nacked the message and why.
func (m *Message) decision() (nacked bool, nackErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nacked, m.nackErr
}

func (m *Message) Ack(_ context.Context) error           { return nil }
func (m *Message) Nack(_ context.Context, _ error) error { return nil }

// New creates a new memory bus.
func New(_ context.Context, cfg eventbus.Config) (eventbus.Bus, error) {
	return &Bus{
		topics: make(map[string]*topic),
		cfg:    cfg,
	}, nil
}

func (b *Bus) bufferSize() int {
	if b.cfg.Memory.BufferSize > 0 {
		return b.cfg.Memory.BufferSize
	}
	return 10000
}

// cloneEnvelope returns a deep copy of env. Envelope contains reference fields
// (Headers and Body), so a shallow value copy still aliases the publisher's
// data. Every party that processes an envelope (the bus, each handler and the
// DLQ) must own an independent copy to avoid map races and lost dlq_reason
// values.
func cloneEnvelope(env eventbus.Envelope) eventbus.Envelope {
	if env.Headers != nil {
		headers := make(map[string]string, len(env.Headers))
		for k, v := range env.Headers {
			headers[k] = v
		}
		env.Headers = headers
	}
	if env.Body != nil {
		body := make([]byte, len(env.Body))
		copy(body, env.Body)
		env.Body = body
	}
	return env
}

func (b *Bus) getOrCreateTopic(name string) *topic {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.topics[name]
	if !ok {
		t = &topic{
			name: name,
			ch:   make(chan eventbus.Envelope, b.bufferSize()),
		}
		b.topics[name] = t
	}
	return t
}

// Publish sends envelopes to the in-process channel for the given topic.
func (b *Bus) Publish(ctx context.Context, topicName string, events []eventbus.Envelope) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return eventbus.ErrBusClosed
	}
	b.mu.RUnlock()

	t := b.getOrCreateTopic(topicName)

	for _, env := range events {
		// Take ownership of the data at the boundary so a caller reusing its
		// Headers/Body cannot race with the consumer goroutines.
		env = cloneEnvelope(env)
		select {
		case t.ch <- env:
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("memory bus: topic %q buffer full", topicName)
		}
	}

	return nil
}

// Subscribe registers a handler for the given topic and consumer group.
func (b *Bus) Subscribe(_ context.Context, topicName string, group string, handler eventbus.Handler) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return eventbus.ErrBusClosed
	}
	b.mu.RUnlock()

	t := b.getOrCreateTopic(topicName)

	t.mu.Lock()
	// Check for duplicate group subscription
	for _, h := range t.handlers {
		if h.group == group {
			t.mu.Unlock()
			return fmt.Errorf("memory bus: group %q already subscribed to topic %q", group, topicName)
		}
	}
	t.handlers = append(t.handlers, handlerEntry{group: group, handler: handler})

	// Start consumer goroutine if not already started
	if !t.started {
		t.started = true
		go b.consumeLoop(t)
	}
	t.mu.Unlock()

	return nil
}

func (b *Bus) consumeLoop(t *topic) {
	for env := range t.ch {
		t.mu.RLock()
		handlers := make([]handlerEntry, len(t.handlers))
		copy(handlers, t.handlers)
		t.mu.RUnlock()

		for _, h := range handlers {
			// Each handler receives its own deep copy so mutations by one
			// handler cannot be observed by another or by the DLQ.
			msg := &Message{
				id:       env.ID,
				topic:    t.name,
				envelope: cloneEnvelope(env),
			}
			// The returned error is the primary failure signal; an explicit
			// Nack is the alternative for handlers that prefer the Message API.
			handlerErr := h.handler(context.Background(), msg)
			nacked, nackErr := msg.decision()

			var failure error
			switch {
			case handlerErr != nil:
				failure = handlerErr
			case nacked:
				failure = nackErr
				if failure == nil {
					failure = errNack
				}
			}

			if failure != nil {
				// Hand the failure path an independent copy so retries and the
				// DLQ never mutate the shared envelope Headers/Body maps.
				b.handleFailure(t, cloneEnvelope(env), failure)
			}
		}
	}
}

// handleFailure requeues an envelope for another attempt while the retry
// budget remains, otherwise it moves the envelope to the DLQ.
func (b *Bus) handleFailure(t *topic, env eventbus.Envelope, reason error) {
	if env.Attempts >= b.cfg.Memory.MaxRetries {
		b.appendToDLQ(env, reason)
		return
	}

	env.Attempts++

	// Do not send on a channel that Close may have closed. Holding the read
	// lock blocks Close from closing the channel while we enqueue, and the
	// non-blocking send keeps this path from stalling delivery.
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.closed {
		select {
		case t.ch <- env:
			return
		default:
		}
	}

	// Bus closed or buffer full: there is no room for another attempt.
	b.appendToDLQ(env, reason)
}

// appendToDLQ records a failed envelope in the in-process DLQ.
func (b *Bus) appendToDLQ(env eventbus.Envelope, reason error) {
	env.Attempts++
	if env.Headers == nil {
		env.Headers = make(map[string]string)
	}
	env.Headers["dlq_reason"] = reason.Error()
	b.dlqMu.Lock()
	b.dlq = append(b.dlq, env)
	b.dlqMu.Unlock()
}

// Close shuts down the memory bus.
func (b *Bus) Close(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, t := range b.topics {
		close(t.ch)
	}
	return nil
}

// Health returns the health status.
func (b *Bus) Health(_ context.Context) eventbus.Health {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return eventbus.Health{OK: false, Detail: "bus closed"}
	}
	return eventbus.Health{OK: true, Detail: "memory"}
}

// PublishDLQ sends an envelope to the in-process DLQ.
func (b *Bus) PublishDLQ(_ context.Context, original eventbus.Envelope, reason error) error {
	// Clone before recording so dlq_reason is never written into the caller's
	// shared Headers map (MASS-0178).
	b.appendToDLQ(cloneEnvelope(original), reason)
	b.dlqMu.Lock()
	defer b.dlqMu.Unlock()
	original.Attempts++
	if original.Headers == nil {
		original.Headers = make(map[string]string)
	}
	original.Headers["dlq_reason"] = eventbus.DLQReasonString(reason)
	b.dlq = append(b.dlq, original)
	return nil
}

// DLQMessages returns the current DLQ messages (for testing/debugging).
func (b *Bus) DLQMessages() []eventbus.Envelope {
	b.dlqMu.Lock()
	defer b.dlqMu.Unlock()
	out := make([]eventbus.Envelope, len(b.dlq))
	for i := range b.dlq {
		out[i] = cloneEnvelope(b.dlq[i])
	}
	return out
}

var _ eventbus.Bus = (*Bus)(nil)
var _ eventbus.DeadLetterPublisher = (*Bus)(nil)

// MarshalEnvelope serializes an Envelope to JSON bytes.
func MarshalEnvelope(env eventbus.Envelope) ([]byte, error) {
	return json.Marshal(env)
}

// UnmarshalEnvelope deserializes an Envelope from JSON bytes.
func UnmarshalEnvelope(data []byte) (eventbus.Envelope, error) {
	var env eventbus.Envelope
	err := json.Unmarshal(data, &env)
	return env, err
}
