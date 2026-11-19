package kafka

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	collectorevent "github.com/astraive/loza/collector/internal/event"
	"github.com/twmb/franz-go/pkg/kgo"
)

// maxRetryBackoffFactor caps exponential retry growth at this multiple of the
// configured base delay, mirroring the bounded range of franz-go's default
// jittery exponential backoff.
const maxRetryBackoffFactor = 32

// cappedRetryBackoff returns base * 2^n, capped at base*maxRetryBackoffFactor.
// It is deterministic so the growth and cap behavior can be tested directly.
func cappedRetryBackoff(base time.Duration, n int) time.Duration {
	if n < 0 {
		n = 0
	}
	max := time.Duration(math.MaxInt64)
	if base <= time.Duration(math.MaxInt64)/maxRetryBackoffFactor {
		max = base * maxRetryBackoffFactor
	}
	d := base
	for i := 0; i < n; i++ {
		if d >= max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// retryBackoffFn returns a kgo.RetryBackoffFn that grows the delay exponentially
// with the retry number and applies jitter. Returning a constant delay makes
// every collector retry in lockstep during a broker outage (thundering herd);
// exponential growth plus jitter spreads retries out over time.
func retryBackoffFn(base time.Duration) func(int) time.Duration {
	return func(n int) time.Duration {
		d := cappedRetryBackoff(base, n)
		if d <= 0 {
			return 0
		}
		// Equal jitter: uniform in [d/2, d]. This preserves the exponential
		// growth while desynchronizing concurrent collector instances.
		half := d / 2
		return half + time.Duration(rand.Int63n(int64(d-half)+1))
	}
}

// Config controls Kafka sink behavior.
type Config struct {
	Brokers []string
	Topic   string
	Acks    string
	// RequestTimeout bounds producer requests.
	RequestTimeout time.Duration
	// EnableIdempotence controls idempotent producer semantics.
	EnableIdempotence bool
	// MaxRetries bounds producer retry attempts.
	MaxRetries int
	// RetryBackoff controls retry delay between transient failures.
	RetryBackoff time.Duration
	// TLSConfig enables TLS for Kafka broker connections when provided.
	TLSConfig *tls.Config
}

type sink struct {
	client *kgo.Client
	topic  string
}

// New creates a Kafka sink.
func New(cfg Config) (collectorevent.Sink, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: Brokers is required")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafka: Topic is required")
	}

	acks := strings.ToLower(strings.TrimSpace(cfg.Acks))
	var requiredAcks kgo.Acks
	switch acks {
	case "", "all":
		requiredAcks = kgo.AllISRAcks()
	case "1":
		requiredAcks = kgo.LeaderAck()
	case "0":
		requiredAcks = kgo.NoAck()
	default:
		return nil, fmt.Errorf("kafka: invalid acks %q (expected 0, 1, all)", cfg.Acks)
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(requiredAcks),
	}
	if cfg.TLSConfig != nil {
		opts = append(opts, kgo.DialTLSConfig(cfg.TLSConfig.Clone()))
	}
	if cfg.RequestTimeout > 0 {
		opts = append(opts, kgo.ProduceRequestTimeout(cfg.RequestTimeout))
	}
	if cfg.MaxRetries > 0 {
		opts = append(opts, kgo.RecordRetries(cfg.MaxRetries), kgo.RequestRetries(cfg.MaxRetries))
	}
	if cfg.RetryBackoff > 0 {
		opts = append(opts, kgo.RetryBackoffFn(retryBackoffFn(cfg.RetryBackoff)))
	}
	if !cfg.EnableIdempotence {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &sink{client: client, topic: cfg.Topic}, nil
}

func (s *sink) Name() string { return "kafka" }

func (s *sink) WriteEvent(ctx context.Context, encoded []byte, _ *collectorevent.Event) error {
	rec := &kgo.Record{Topic: s.topic, Value: encoded}
	res := s.client.ProduceSync(ctx, rec)
	return res.FirstErr()
}

func (s *sink) Flush(ctx context.Context) error {
	return s.client.Flush(ctx)
}

func (s *sink) Close(_ context.Context) error {
	s.client.Close()
	return nil
}
