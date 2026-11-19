// Package nats implements the NATS JetStream event bus adapter.
// This is the recommended production default for Loza.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func init() {
	eventbus.Register("nats", New)
}

const (
	// iterErrorInitialBackoff is the delay before retrying after the first
	// iterator error. It grows exponentially up to iterErrorMaxBackoff.
	iterErrorInitialBackoff = 50 * time.Millisecond
	iterErrorMaxBackoff     = 2 * time.Second
)

// messageIterator is the subset of jetstream.MessagesContext used by the
// consume loop. Keeping it narrow lets the loop be unit-tested without a
// running NATS server.
type messageIterator interface {
	Next(opts ...jetstream.NextOpt) (jetstream.Msg, error)
}

// stoppableIterator is a messageIterator that also supports Stop. The iterator
// returned by consumer.Messages implements it; the bus records Stop as a
// shutdown hook so Close can invalidate a pending iterator.
type stoppableIterator interface {
	messageIterator
	Stop()
}

// sleepFunc waits for d or until ctx is done. It reports whether the wait
// completed; false tells the caller to stop consuming.
type sleepFunc func(ctx context.Context, d time.Duration) bool

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type natsBus struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	cfg    eventbus.NATSConfig
	dlqCfg string
	closed atomic.Bool
	mu     sync.Mutex
	// ctx is the bus lifecycle context. Subscription contexts derive from it so
	// cancelling the bus stops every consumer through one cancel.
	ctx    context.Context
	subs   []subscription
	cancel context.CancelFunc
}

// subscription holds the shutdown hooks for one Subscribe call. cancel stops
// the consumer's context (unblocking a pending iter.Next via NextContext) and
// stop invalidates the message iterator.
type subscription struct {
	cancel context.CancelFunc
	stop   func()
}

// New creates a new NATS JetStream event bus.
func New(ctx context.Context, cfg eventbus.Config) (eventbus.Bus, error) {
	nc := cfg.NATS
	if nc.URL == "" {
		nc.URL = nats.DefaultURL
	}
	if nc.Stream == "" {
		nc.Stream = "LOZA"
	}
	if nc.Subject == "" {
		nc.Subject = cfg.Topic
	}
	if nc.Subject == "" {
		nc.Subject = "loza.events.raw"
	}
	if nc.Durable == "" {
		nc.Durable = cfg.ConsumerGroup
	}
	if nc.Durable == "" {
		nc.Durable = "loza-worker"
	}

	opts := []nats.Option{nats.Name("loza-collector")}
	if nc.Username != "" && nc.Password != "" {
		opts = append(opts, nats.UserInfo(nc.Username, nc.Password))
	}

	conn, err := nats.Connect(nc.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("eventbus/nats: connect: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("eventbus/nats: jetstream: %w", err)
	}

	// Ensure stream exists
	subjects := []string{nc.Subject}
	if cfg.DLQTopic != "" {
		subjects = append(subjects, cfg.DLQTopic)
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      nc.Stream,
		Subjects:  subjects,
		Retention: jetstream.WorkQueuePolicy,
		Storage:   jetstream.FileStorage,
		MaxAge:    7 * 24 * time.Hour, // 7 days retention
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("eventbus/nats: create stream: %w", err)
	}

	busCtx, cancel := context.WithCancel(context.Background())

	return &natsBus{
		conn:   conn,
		js:     js,
		cfg:    nc,
		dlqCfg: cfg.DLQTopic,
		ctx:    busCtx,
		cancel: cancel,
	}, nil
}

func (b *natsBus) Publish(ctx context.Context, topic string, events []eventbus.Envelope) error {
	if b.closed.Load() {
		return eventbus.ErrBusClosed
	}

	subject := topic
	if subject == "" {
		subject = b.cfg.Subject
	}

	for _, env := range events {
		data, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("eventbus/nats: marshal envelope: %w", err)
		}
		_, err = b.js.Publish(ctx, subject, data)
		if err != nil {
			return fmt.Errorf("eventbus/nats: publish: %w", err)
		}
	}
	return nil
}

func (b *natsBus) Subscribe(ctx context.Context, topic string, group string, handler eventbus.Handler) error {
	if b.closed.Load() {
		return eventbus.ErrBusClosed
	}

	subject := topic
	if subject == "" {
		subject = b.cfg.Subject
	}
	durable := group
	if durable == "" {
		durable = b.cfg.Durable
	}

	// Derive the consumer's lifetime context from the bus context so that
	// Close stops every consumer, while the bus itself stays alive.
	subCtx, cancel := context.WithCancel(b.ctx)

	consumer, err := b.js.CreateOrUpdateConsumer(ctx, b.cfg.Stream, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    5,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("eventbus/nats: create consumer: %w", err)
	}

	iter, err := consumer.Messages(jetstream.PullMaxMessages(10))
	if err != nil {
		cancel()
		return fmt.Errorf("eventbus/nats: messages: %w", err)
	}

	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		cancel()
		iter.Stop()
		return eventbus.ErrBusClosed
	}
	b.subs = append(b.subs, subscription{cancel: cancel, stop: iter.Stop})
	b.mu.Unlock()

	go b.consume(subCtx, iter, subject, handler)

	return nil
}

// consumeMessages drains iter and dispatches messages to handler. An error
// from iter.Next() can be persistent (e.g. a deleted consumer or a lost
// connection) and would otherwise make the loop spin with no delay. Apply an
// exponential backoff, capped at maxBackoff, before retrying. A successful
// message resets the backoff.
//
// NextContext is used so a blocked iter.Next returns promptly when ctx is
// cancelled; combined with the closed flag this gives clean shutdown (the
// subscription lifecycle wired up by Subscribe/Close).
func consumeMessages(ctx context.Context, closed *atomic.Bool, iter messageIterator, subject string, handler eventbus.Handler, sleep sleepFunc, initialBackoff, maxBackoff time.Duration) {
	backoff := initialBackoff
	for {
		msg, err := iter.Next(jetstream.NextContext(ctx))
		if err != nil {
			if closed.Load() || ctx.Err() != nil {
				return
			}
			if !sleep(ctx, backoff) || closed.Load() {
				return
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = initialBackoff

		handleMessage(ctx, msg, subject, handler)
	}
}

// handleMessage delivers one JetStream message to the handler.
//
// Acknowledgment is owned exclusively by the handler through Message.Ack and
// Message.Nack. The handler's return value is deliberately not used to ack or
// nack the message: a handler may defer Ack until a downstream commit, may call
// Nack and still return nil, or may call Ack and still return an error. Using
// the return value here produced double acknowledgments and acked messages the
// handler had nacked.
func handleMessage(ctx context.Context, msg jetstream.Msg, subject string, handler eventbus.Handler) {
	var env eventbus.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		// A malformed envelope never reaches the handler, so the adapter nacks it.
		_ = msg.Nak()
		return
	}

	busMsg := &natsMessage{
		id:    env.ID,
		topic: subject,
		env:   env,
		msg:   msg,
	}
	// Informational only: never convert the return value into an adapter ack.
	_ = handler(ctx, busMsg)
}

// consume is the bus-owned consume loop. It wires the subscription lifecycle
// (the bus closed flag and context cancellation) into consumeMessages, using
// the standard capped iterator backoff settings.
func (b *natsBus) consume(ctx context.Context, iter stoppableIterator, subject string, handler eventbus.Handler) {
	consumeMessages(ctx, &b.closed, iter, subject, handler, sleepWithContext,
		iterErrorInitialBackoff, iterErrorMaxBackoff)
}

func (b *natsBus) Close(_ context.Context) error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	b.mu.Lock()
	for _, sub := range b.subs {
		sub.cancel()
		sub.stop()
	}
	b.subs = nil
	b.mu.Unlock()
	b.cancel()
	if b.conn != nil {
		b.conn.Close()
	}
	return nil
}

func (b *natsBus) Health(_ context.Context) eventbus.Health {
	if b.closed.Load() {
		return eventbus.Health{OK: false, Detail: "closed"}
	}
	if !b.conn.IsConnected() {
		return eventbus.Health{OK: false, Detail: "disconnected"}
	}
	return eventbus.Health{OK: true}
}

func (b *natsBus) PublishDLQ(ctx context.Context, original eventbus.Envelope, reason error) error {
	dlqTopic := b.dlqCfg
	if dlqTopic == "" {
		dlqTopic = "loza.events.dlq"
	}
	if original.Headers == nil {
		original.Headers = make(map[string]string)
	}
	original.Headers["dlq_reason"] = eventbus.DLQReasonString(reason)
	if reason != nil {
		original.Headers["dlq_reason"] = reason.Error()
	}
	original.Headers["dlq_time"] = time.Now().UTC().Format(time.RFC3339Nano)
	return b.Publish(ctx, dlqTopic, []eventbus.Envelope{original})
}

type natsMessage struct {
	id    string
	topic string
	env   eventbus.Envelope
	msg   jetstream.Msg
}

func (m *natsMessage) ID() string                            { return m.id }
func (m *natsMessage) Topic() string                         { return m.topic }
func (m *natsMessage) Envelope() eventbus.Envelope           { return m.env }
func (m *natsMessage) Ack(_ context.Context) error           { return m.msg.Ack() }
func (m *natsMessage) Nack(_ context.Context, _ error) error { return m.msg.Nak() }
