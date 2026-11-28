// Package redis implements the Redis Streams event bus adapter.
// Good for simple self-hosted deployments with Docker Compose.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

func init() {
	eventbus.Register("redis", New)
}

// maxRedisDeliveryAttempts is the number of times a message whose handler keeps
// failing is redelivered before it is moved to the dead-letter stream.
const maxRedisDeliveryAttempts = 3

const redisNackTimeout = 2 * time.Second

const redisReadBlock = time.Second

// Pending entries are reclaimed only after this idle period. Active handlers
// refresh their pending-entry claim before the threshold expires.
const redisPendingClaimIdle = 30 * time.Second

const redisPendingClaimInterval = 5 * time.Second

const redisClaimHeartbeatInterval = 10 * time.Second

const redisPendingClaimCount = 10

var errNackWithoutCause = errors.New("eventbus/redis: message nacked without cause")

type redisBus struct {
	client                 *redis.Client
	cfg                    eventbus.RedisConfig
	dlqCfg                 string
	closed                 atomic.Bool
	closing                atomic.Bool
	mu                     sync.Mutex
	subs                   []context.CancelFunc
	subsWg                 sync.WaitGroup
	pendingClaimIdle       time.Duration
	pendingClaimInterval   time.Duration
	claimHeartbeatInterval time.Duration
}

// New creates a new Redis Streams event bus.
func New(_ context.Context, cfg eventbus.Config) (eventbus.Bus, error) {
	rc := cfg.Redis
	if rc.Addr == "" {
		rc.Addr = "127.0.0.1:6379"
	}
	if rc.Stream == "" {
		rc.Stream = cfg.Topic
	}
	if rc.Stream == "" {
		rc.Stream = "loza.events.raw"
	}
	if rc.Group == "" {
		rc.Group = cfg.ConsumerGroup
	}
	if rc.Group == "" {
		rc.Group = "loza-worker"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     rc.Addr,
		Password: rc.Password,
		DB:       rc.DB,
	})

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("eventbus/redis: ping: %w", err)
	}

	// Ensure consumer group exists
	err := client.XGroupCreateMkStream(ctx, rc.Stream, rc.Group, "0").Err()
	if err != nil && !isBusyGroupErr(err) {
		client.Close()
		return nil, fmt.Errorf("eventbus/redis: create group: %w", err)
	}

	return &redisBus{
		client:                 client,
		cfg:                    rc,
		dlqCfg:                 cfg.DLQTopic,
		pendingClaimIdle:       redisPendingClaimIdle,
		pendingClaimInterval:   redisPendingClaimInterval,
		claimHeartbeatInterval: redisClaimHeartbeatInterval,
	}, nil
}

func (b *redisBus) Publish(ctx context.Context, topic string, events []eventbus.Envelope) error {
	if b.closed.Load() || b.closing.Load() {
		return eventbus.ErrBusClosed
	}
	return b.publish(ctx, topic, events)
}

func (b *redisBus) publish(ctx context.Context, topic string, events []eventbus.Envelope) error {
	if b.closed.Load() {
		return eventbus.ErrBusClosed
	}

	stream := topic
	if stream == "" {
		stream = b.cfg.Stream
	}

	maxLen := b.cfg.MaxLen
	if maxLen <= 0 {
		maxLen = 1000000
	}

	for _, env := range events {
		data, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("eventbus/redis: marshal: %w", err)
		}
		args := &redis.XAddArgs{
			Stream: stream,
			MaxLen: maxLen,
			Approx: true,
			Values: map[string]any{"body": data},
		}
		if err := b.client.XAdd(ctx, args).Err(); err != nil {
			return fmt.Errorf("eventbus/redis: xadd: %w", err)
		}
	}
	return nil
}

func (b *redisBus) Subscribe(ctx context.Context, topic string, group string, handler eventbus.Handler) error {
	if b.closed.Load() || b.closing.Load() {
		return eventbus.ErrBusClosed
	}

	stream := topic
	if stream == "" {
		stream = b.cfg.Stream
	}
	consumerGroup := group
	if consumerGroup == "" {
		consumerGroup = b.cfg.Group
	}

	consumerName := fmt.Sprintf("consumer-%d", time.Now().UnixNano())

	// Create consumer group if it doesn't exist (MkStream creates the stream too)
	if err := b.client.XGroupCreateMkStream(ctx, stream, consumerGroup, "0").Err(); err != nil {
		// BUSYGROUP means the group already exists, which is fine
		if !isBusyGroupErr(err) {
			return fmt.Errorf("eventbus/redis: create group: %w", err)
		}
	}

	subCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	if b.closed.Load() || b.closing.Load() {
		b.mu.Unlock()
		cancel()
		return eventbus.ErrBusClosed
	}
	b.subs = append(b.subs, cancel)
	b.subsWg.Add(1)
	b.mu.Unlock()

	go func() {
		defer b.subsWg.Done()
		b.consume(subCtx, stream, consumerGroup, consumerName, handler)
	}()
	return nil
}

func isBusyGroupErr(err error) bool {
	if err == nil {
		return false
	}
	// go-redis reports server replies as redis.Error; HasErrorPrefix looks
	// through wrapping (and an optional "ERR " prefix). Fall back to the
	// rendered message so driver/version differences don't make startup fail
	// spuriously when the group already exists.
	return redis.HasErrorPrefix(err, "BUSYGROUP") || strings.HasPrefix(err.Error(), "BUSYGROUP")
}

func (b *redisBus) consume(ctx context.Context, stream, group, consumer string, handler eventbus.Handler) {
	var nextPendingClaim time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if nextPendingClaim.IsZero() || !time.Now().Before(nextPendingClaim) {
			if !b.claimPending(ctx, stream, group, consumer, handler) {
				return
			}
			nextPendingClaim = time.Now().Add(b.pendingClaimInterval)
		}

		streams, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    10,
			Block:    redisReadBlock,
		}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err == redis.Nil {
				continue
			}
			time.Sleep(time.Second)
			continue
		}

		for _, s := range streams {
			for _, msg := range s.Messages {
				if !b.handleMessage(ctx, stream, group, consumer, msg, handler) {
					return
				}
			}
		}
	}
}

func (b *redisBus) claimPending(
	ctx context.Context,
	stream, group, consumer string,
	handler eventbus.Handler,
) bool {
	pending, err := b.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Idle:   b.pendingClaimIdle,
		Start:  "-",
		End:    "+",
		Count:  redisPendingClaimCount,
	}).Result()
	if err != nil {
		if ctx.Err() == nil {
			log.Error().Err(err).Str("stream", stream).Str("group", group).
				Msg("failed to inspect Redis pending entries")
		}
		return ctx.Err() == nil
	}
	if len(pending) == 0 {
		return true
	}

	ids := make([]string, 0, len(pending))
	for _, entry := range pending {
		ids = append(ids, entry.ID)
	}
	messages, err := b.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  b.pendingClaimIdle,
		Messages: ids,
	}).Result()
	if err != nil {
		if ctx.Err() == nil {
			log.Error().Err(err).Str("stream", stream).Str("group", group).
				Msg("failed to claim stale Redis pending entries")
		}
		return ctx.Err() == nil
	}
	for _, msg := range messages {
		if !b.handleMessage(ctx, stream, group, consumer, msg, handler) {
			return false
		}
	}
	return true
}

func (b *redisBus) handleMessage(
	ctx context.Context,
	stream, group, consumer string,
	msg redis.XMessage,
	handler eventbus.Handler,
) bool {
	if ctx.Err() != nil {
		return false
	}
	env, ok := decodeMessage(msg)
	if !ok {
		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), redisNackTimeout)
		err := b.client.XAck(ackCtx, stream, group, msg.ID).Err()
		ackCancel()
		if err != nil {
			log.Error().Err(err).Str("stream", stream).Str("message_id", msg.ID).
				Msg("failed to acknowledge malformed Redis message")
		}
		return ctx.Err() == nil
	}

	busMsg := &redisMessage{
		id:       env.ID,
		topic:    stream,
		env:      env,
		client:   b.client,
		stream:   stream,
		group:    group,
		streamID: msg.ID,
		bus:      b,
	}
	stopHeartbeat := b.keepClaimAlive(ctx, stream, group, consumer, msg.ID)
	handlerErr := func() error {
		defer stopHeartbeat()
		return handler(ctx, busMsg)
	}()
	if handlerErr != nil {
		// An interrupted handler was not committed. Leave it pending so a later
		// consumer can claim it without spending the retry budget.
		if ctx.Err() != nil {
			return false
		}
		nackCtx, nackCancel := context.WithTimeout(context.WithoutCancel(ctx), redisNackTimeout)
		err := busMsg.Nack(nackCtx, handlerErr)
		nackCancel()
		if err != nil {
			log.Error().Err(err).Str("stream", stream).Str("message_id", msg.ID).
				Msg("failed to release Redis message; it remains pending for recovery")
		}
		return ctx.Err() == nil
	}
	if ctx.Err() != nil {
		return false
	}
	if err := busMsg.Ack(ctx); err != nil {
		log.Error().Err(err).Str("stream", stream).Str("message_id", msg.ID).
			Msg("failed to acknowledge Redis message; it remains pending for recovery")
	}
	return ctx.Err() == nil
}

// keepClaimAlive refreshes an active consumer's pending-entry idle time so
// another consumer cannot reclaim a long-running handler. JUSTID avoids
// incrementing Redis's delivery count for these lease renewals.
func (b *redisBus) keepClaimAlive(
	ctx context.Context,
	stream, group, consumer, messageID string,
) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(b.claimHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(heartbeatCtx, redisNackTimeout)
				_, err := b.client.XClaimJustID(renewCtx, &redis.XClaimArgs{
					Stream:   stream,
					Group:    group,
					Consumer: consumer,
					MinIdle:  0,
					Messages: []string{messageID},
				}).Result()
				renewCancel()
				if err != nil && heartbeatCtx.Err() == nil {
					log.Error().Err(err).Str("stream", stream).Str("group", group).
						Str("consumer", consumer).Str("message_id", messageID).
						Msg("failed to refresh active Redis message claim")
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// decodeMessage extracts the envelope from a Redis stream entry.
func decodeMessage(msg redis.XMessage) (eventbus.Envelope, bool) {
	bodyRaw, ok := msg.Values["body"]
	if !ok {
		return eventbus.Envelope{}, false
	}
	body, ok := bodyRaw.(string)
	if !ok {
		return eventbus.Envelope{}, false
	}
	var env eventbus.Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return eventbus.Envelope{}, false
	}
	return env, true
}

// failMessage releases a message whose handler failed. It re-publishes the
// message to the stream for a bounded number of retry attempts and routes it
// to the DLQ once the retry budget is exhausted. The original pending entry is
// always acknowledged so the consumer group's pending list does not grow.
func (b *redisBus) failMessage(ctx context.Context, stream, group, streamID string, env eventbus.Envelope, cause error) error {
	if cause == nil {
		cause = errNackWithoutCause
	}
	env.Attempts++
	if env.Attempts >= maxRedisDeliveryAttempts {
		if dlqErr := b.publishDLQ(ctx, env, cause); dlqErr == nil {
			return b.client.XAck(ctx, stream, group, streamID).Err()
		}
		// If the DLQ is unavailable, requeue instead of losing the message.
	}
	if pubErr := b.publish(ctx, stream, []eventbus.Envelope{env}); pubErr != nil {
		return pubErr
	}
	return b.client.XAck(ctx, stream, group, streamID).Err()
}

func (b *redisBus) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closing.Load() {
		b.mu.Unlock()
		return nil
	}
	b.closing.Store(true)
	for _, cancel := range b.subs {
		cancel()
	}
	b.subs = nil
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.subsWg.Wait()
		close(done)
	}()
	var waitErr error
	select {
	case <-done:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}
	b.closed.Store(true)
	return errors.Join(waitErr, b.client.Close())
}

func (b *redisBus) Health(ctx context.Context) eventbus.Health {
	if b.closed.Load() || b.closing.Load() {
		return eventbus.Health{OK: false, Detail: "closed"}
	}
	if err := b.client.Ping(ctx).Err(); err != nil {
		return eventbus.Health{OK: false, Detail: err.Error()}
	}
	return eventbus.Health{OK: true}
}

func (b *redisBus) PublishDLQ(ctx context.Context, original eventbus.Envelope, reason error) error {
	if b.closed.Load() || b.closing.Load() {
		return eventbus.ErrBusClosed
	}
	return b.publishDLQ(ctx, original, reason)
}

func (b *redisBus) publishDLQ(ctx context.Context, original eventbus.Envelope, reason error) error {
	dlqTopic := b.dlqCfg
	if dlqTopic == "" {
		dlqTopic = "loza.events.dlq"
	}
	if original.Headers == nil {
		original.Headers = make(map[string]string)
	}
	original.Headers["dlq_reason"] = eventbus.DLQReasonString(reason)
	original.Headers["dlq_time"] = time.Now().UTC().Format(time.RFC3339Nano)
	return b.publish(ctx, dlqTopic, []eventbus.Envelope{original})
}

type redisMessage struct {
	id       string
	topic    string
	env      eventbus.Envelope
	client   *redis.Client
	stream   string
	group    string
	streamID string
	bus      *redisBus
	settled  atomic.Bool
}

func (m *redisMessage) ID() string                  { return m.id }
func (m *redisMessage) Topic() string               { return m.topic }
func (m *redisMessage) Envelope() eventbus.Envelope { return m.env }

// Ack acknowledges the pending entry so it is not redelivered.
func (m *redisMessage) Ack(ctx context.Context) error {
	if !m.settled.CompareAndSwap(false, true) {
		return nil
	}
	return m.client.XAck(ctx, m.stream, m.group, m.streamID).Err()
}

// Nack releases the pending entry: the message is requeued for another
// delivery attempt and, once the retry budget is exhausted, moved to the DLQ.
func (m *redisMessage) Nack(ctx context.Context, cause error) error {
	if !m.settled.CompareAndSwap(false, true) {
		return nil
	}
	if m.bus == nil {
		return m.client.XAck(ctx, m.stream, m.group, m.streamID).Err()
	}
	return m.bus.failMessage(ctx, m.stream, m.group, m.streamID, m.env, cause)
}
