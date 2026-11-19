// Package kafka implements the Kafka event bus adapter.
// This is the enterprise-grade adapter for high-throughput, long-retention deployments.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

func init() {
	eventbus.Register("kafka", New)
}

type kafkaBus struct {
	client *kgo.Client
	cfg    eventbus.KafkaConfig
	dlqCfg string
	closed atomic.Bool
	mu     sync.Mutex
	subs   []context.CancelFunc
}

// producerOpts builds the producer options used by New. Keeping the option
// construction separate lets config validation be exercised without a broker.
func producerOpts(kc eventbus.KafkaConfig) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(kc.Brokers...),
		kgo.DefaultProduceTopic(kc.Topic),
		kgo.RecordPartitioner(kgo.RoundRobinPartitioner()),
	}

	if !kc.EnableIdempotence {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}

	switch kc.Compression {
	case "snappy":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case "lz4":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case "zstd":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	case "gzip":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	default:
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	}

	switch kc.Acks {
	case "0":
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
	case "1":
		// Idempotent writes require acks=all in franz-go, so opt out even
		// when idempotence was explicitly requested (same as acks "0").
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
	default:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	}

	return opts
}

// New creates a new Kafka event bus.
// Auto-creates topics via the admin API (idempotent).
func New(_ context.Context, cfg eventbus.Config) (eventbus.Bus, error) {
	kc := cfg.Kafka
	if len(kc.Brokers) == 0 {
		return nil, fmt.Errorf("eventbus/kafka: brokers must not be empty")
	}
	if kc.Topic == "" {
		kc.Topic = cfg.Topic
	}
	if kc.Topic == "" {
		kc.Topic = "loza.events.raw"
	}
	if kc.ConsumerGroup == "" {
		kc.ConsumerGroup = cfg.ConsumerGroup
	}
	if kc.ConsumerGroup == "" {
		kc.ConsumerGroup = "loza-worker"
	}

	client, err := kgo.NewClient(producerOpts(kc)...)
	if err != nil {
		return nil, fmt.Errorf("eventbus/kafka: client: %w", err)
	}

	// Verify connectivity and create topics
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adm := kadm.NewClient(client)

	topicNames := []string{kc.Topic}
	if cfg.DLQTopic != "" {
		topicNames = append(topicNames, cfg.DLQTopic)
	}
	resps, createErr := adm.CreateTopics(ctx, 1, 1, nil, topicNames...)
	if createErr != nil {
		client.Close()
		return nil, fmt.Errorf("eventbus/kafka: create topics: %w", createErr)
	}
	for _, resp := range resps.Sorted() {
		if resp.Err != nil && !isTopicAlreadyExists(resp.Err) {
			// kadm.CreateTopics returns per-topic errors; a TOPIC_ALREADY_EXISTS
			// (code 36) response is benign and expected on restart. Any other
			// error is fatal.
			client.Close()
			return nil, fmt.Errorf("eventbus/kafka: create topic %q: %w", resp.Topic, resp.Err)
		}
	}

	return &kafkaBus{
		client: client,
		cfg:    kc,
		dlqCfg: cfg.DLQTopic,
	}, nil
}

// firstProduceErr records the first non-nil error reported by concurrent
// produce callbacks. Produce errors within a single batch can have different
// concrete types (e.g. *kerr.Error and kgo.ErrRecordTimeout), so they must not
// be stored in a single atomic.Value: atomic.Value.CompareAndSwap panics with
// "inconsistently typed value into Value" when the new value has a different
// dynamic type than the value already stored. A mutex guarding a plain error
// avoids that panic while still keeping only the first failure.
type firstProduceErr struct {
	mu  sync.Mutex
	err error
}

func (f *firstProduceErr) set(err error) {
	if err == nil {
		return
	}
	f.mu.Lock()
	if f.err == nil {
		f.err = err
	}
	f.mu.Unlock()
}

func (f *firstProduceErr) load() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// isTopicAlreadyExists reports whether err is the Kafka TOPIC_ALREADY_EXISTS
// error (code 36). The typed kerr code is used instead of matching message
// text, which can differ between broker/client versions and must not turn a
// benign "topic already exists" response into a startup failure.
func isTopicAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	var kerrErr *kerr.Error
	return errors.As(err, &kerrErr) && kerrErr.Code == kerr.TopicAlreadyExists.Code
}

func (b *kafkaBus) Publish(ctx context.Context, topic string, events []eventbus.Envelope) error {
	if b.closed.Load() {
		return eventbus.ErrBusClosed
	}

	produceTopic := topic
	if produceTopic == "" {
		produceTopic = b.cfg.Topic
	}

	var wg sync.WaitGroup
	var firstErr firstProduceErr

	for _, env := range events {
		data, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("eventbus/kafka: marshal: %w", err)
		}
		wg.Add(1)
		record := &kgo.Record{
			Topic: produceTopic,
			Key:   []byte(env.ID),
			Value: data,
		}
		b.client.Produce(ctx, record, func(r *kgo.Record, err error) {
			defer wg.Done()
			firstErr.set(err)
		})
	}

	wg.Wait()
	if err := firstErr.load(); err != nil {
		return fmt.Errorf("eventbus/kafka: produce: %w", err)
	}
	return nil
}

func (b *kafkaBus) Subscribe(ctx context.Context, topic string, group string, handler eventbus.Handler) error {
	if b.closed.Load() {
		return eventbus.ErrBusClosed
	}

	consumeTopic := topic
	if consumeTopic == "" {
		consumeTopic = b.cfg.Topic
	}
	consumerGroup := group
	if consumerGroup == "" {
		consumerGroup = b.cfg.ConsumerGroup
	}

	ready := make(chan struct{})
	var once sync.Once

	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(b.cfg.Brokers...),
		kgo.ConsumeTopics(consumeTopic),
		kgo.ConsumerGroup(consumerGroup),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.SessionTimeout(45*time.Second),
		kgo.OnPartitionsAssigned(func(_ context.Context, _ *kgo.Client, _ map[string][]int32) {
			once.Do(func() { close(ready) })
		}),
		kgo.OnPartitionsRevoked(func(_ context.Context, _ *kgo.Client, _ map[string][]int32) {}),
		kgo.OnPartitionsLost(func(_ context.Context, _ *kgo.Client, _ map[string][]int32) {}),
	)
	if err != nil {
		return fmt.Errorf("eventbus/kafka: consumer: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.subs = append(b.subs, cancel)
	b.mu.Unlock()

	go func() {
		defer consumer.Close()
		for {
			if subCtx.Err() != nil {
				return
			}
			fetches := consumer.PollFetches(subCtx)
			if subCtx.Err() != nil {
				return
			}
			fetches.EachRecord(func(rec *kgo.Record) {
				var env eventbus.Envelope
				if err := json.Unmarshal(rec.Value, &env); err != nil {
					return
				}
				msg := &message{
					id:       env.ID,
					topic:    rec.Topic,
					envelope: env,
					consumer: consumer,
					rec:      rec,
				}
				if err := handler(subCtx, msg); err != nil {
					// Route to DLQ
					if dlqErr := b.PublishDLQ(subCtx, env, err); dlqErr != nil {
						log.Error().Err(dlqErr).Str("topic", rec.Topic).Msg("failed to publish to DLQ, message will be redelivered")
					}
					return
				}
				consumer.CommitRecords(subCtx, rec)
			})
		}
	}()

	// Wait for the consumer group to join and partitions to be assigned.
	// This callback fires inside PollFetches during the group join protocol.
	select {
	case <-ready:
		return nil
	case <-time.After(30 * time.Second):
		cancel()
		return fmt.Errorf("eventbus/kafka: subscribe timeout: consumer group did not become ready within 30s")
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

func (b *kafkaBus) Close(_ context.Context) error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	b.mu.Lock()
	for _, cancel := range b.subs {
		cancel()
	}
	b.mu.Unlock()
	b.client.Close()
	return nil
}

func (b *kafkaBus) Health(ctx context.Context) eventbus.Health {
	if b.closed.Load() {
		return eventbus.Health{OK: false, Detail: "closed"}
	}
	// Verify broker connectivity by requesting metadata
	adm := kadm.NewClient(b.client)
	topics, err := adm.ListTopics(ctx)
	if err != nil {
		return eventbus.Health{OK: false, Detail: fmt.Sprintf("broker unreachable: %v", err)}
	}
	_ = topics
	return eventbus.Health{OK: true}
}

func (b *kafkaBus) PublishDLQ(ctx context.Context, original eventbus.Envelope, reason error) error {
	dlqTopic := b.dlqCfg
	if dlqTopic == "" {
		dlqTopic = "loza.events.dlq"
	}
	if original.Headers == nil {
		original.Headers = make(map[string]string)
	}
	original.Headers["dlq_reason"] = reason.Error()
	original.Headers["dlq_time"] = time.Now().UTC().Format(time.RFC3339Nano)
	return b.Publish(ctx, dlqTopic, []eventbus.Envelope{original})
}

type message struct {
	id       string
	topic    string
	envelope eventbus.Envelope
	consumer *kgo.Client
	rec      *kgo.Record
}

func (m *message) ID() string                            { return m.id }
func (m *message) Topic() string                         { return m.topic }
func (m *message) Envelope() eventbus.Envelope           { return m.envelope }
func (m *message) Ack(ctx context.Context) error         { return m.consumer.CommitRecords(ctx, m.rec) }
func (m *message) Nack(_ context.Context, _ error) error { return nil }

var _ eventbus.Bus = (*kafkaBus)(nil)
var _ eventbus.DeadLetterPublisher = (*kafkaBus)(nil)
