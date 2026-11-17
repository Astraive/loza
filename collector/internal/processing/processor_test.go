package processing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	collectorevent "github.com/astraive/loza/collector/internal/event"
	"github.com/redis/go-redis/v9"
)

type fakeSink struct {
	mu     sync.Mutex
	writes int
	errs   []error
}

func (s *fakeSink) Name() string { return "fake" }

func (s *fakeSink) WriteEvent(context.Context, []byte, *collectorevent.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.writes
	s.writes++
	if idx < len(s.errs) {
		return s.errs[idx]
	}
	return nil
}

func (s *fakeSink) Flush(context.Context) error { return nil }
func (s *fakeSink) Close(context.Context) error { return nil }

func (s *fakeSink) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func TestProcessorInvalidEventWritesDLQ(t *testing.T) {
	dlqPath := filepath.Join(t.TempDir(), "dlq.ndjson")
	p, err := New(Config{
		DLQEnabled:          true,
		DLQPath:             dlqPath,
		ValidateJSONObjects: true,
	}, &fakeSink{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	res := p.Process(context.Background(), []byte("123"))
	if !res.Invalid {
		t.Fatalf("expected invalid result")
	}
	if !errors.Is(res.Err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", res.Err)
	}

	raw, err := os.ReadFile(dlqPath)
	if err != nil {
		t.Fatalf("read dlq: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one dlq entry, got %d", len(lines))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode dlq entry: %v", err)
	}
	if !strings.Contains(entry["error"].(string), "invalid event payload") {
		t.Fatalf("unexpected dlq error: %v", entry["error"])
	}
}

func TestProcessorRetryAndDLQOnPrimaryFailure(t *testing.T) {
	dlqPath := filepath.Join(t.TempDir(), "dlq.ndjson")
	primary := &fakeSink{errs: []error{errors.New("primary failed"), errors.New("primary failed")}}
	p, err := New(Config{
		DeliveryPolicy:      "require_primary",
		RetryEnabled:        true,
		RetryMaxAttempts:    2,
		RetryInitialBackoff: time.Millisecond,
		RetryMaxBackoff:     time.Millisecond,
		DLQEnabled:          true,
		DLQPath:             dlqPath,
		DLQOnPrimaryFail:    true,
		ValidateJSONObjects: true,
	}, primary, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	res := p.Process(context.Background(), []byte(`{"event_id":"evt-1","event":"a"}`))
	if res.Err == nil {
		t.Fatalf("expected processing error")
	}
	if got := primary.Writes(); got != 2 {
		t.Fatalf("expected 2 retries, got %d", got)
	}

	raw, err := os.ReadFile(dlqPath)
	if err != nil {
		t.Fatalf("read dlq: %v", err)
	}
	if !strings.Contains(string(raw), "primary failed") {
		t.Fatalf("expected dlq to contain primary failure, got %s", string(raw))
	}
}

func TestProcessorDedupeSkipsSecondWrite(t *testing.T) {
	primary := &fakeSink{}
	p, err := New(Config{
		DedupeEnabled:       true,
		DedupeKey:           "event_id",
		DedupeWindow:        time.Minute,
		ValidateJSONObjects: true,
	}, primary, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	first := p.Process(context.Background(), []byte(`{"event_id":"evt-1","event":"a"}`))
	second := p.Process(context.Background(), []byte(`{"event_id":"evt-1","event":"a"}`))
	if !first.Accepted || first.Deduped {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if !second.Accepted || !second.Deduped {
		t.Fatalf("expected second event to dedupe, got %+v", second)
	}
	if got := primary.Writes(); got != 1 {
		t.Fatalf("expected single sink write, got %d", got)
	}
}

func TestProcessorBestEffortUsesSecondarySuccess(t *testing.T) {
	primary := &fakeSink{errs: []error{errors.New("primary failed")}}
	secondary := &fakeSink{}
	p, err := New(Config{
		DeliveryPolicy:      "best_effort",
		ValidateJSONObjects: true,
	}, primary, []NamedSink{{Name: "secondary", Sink: secondary}}, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	res := p.Process(context.Background(), []byte(`{"event":"a"}`))
	if res.Err != nil {
		t.Fatalf("expected best effort to succeed, got %v", res.Err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted result")
	}
	if res.Outcome.FailureCount() != 1 {
		t.Fatalf("expected one tracked failure, got %d", res.Outcome.FailureCount())
	}
}

// TestNewRedisDedupeClientHonorsContextTimeouts verifies that the Redis client
// built in New is configured to respect the deadlines of the contexts passed
// to its commands (go-redis disables this by default).
func TestNewRedisDedupeClientHonorsContextTimeouts(t *testing.T) {
	mr := miniredis.RunT(t)
	p, err := New(Config{
		DedupeBackend:   "redis",
		DedupeRedisAddr: mr.Addr(),
	}, &fakeSink{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	if p.redisClient == nil {
		t.Fatal("expected a redis dedupe client")
	}
	if !p.redisClient.Options().ContextTimeoutEnabled {
		t.Fatal("redis dedupe client does not honor context timeouts")
	}
}

// TestProcessorRedisDedupeHonorsCanceledContext verifies that the Redis
// dedupe SetNX call is derived from the caller's context. A canceled caller
// must abort the dedupe check instead of silently using context.Background()
// and writing the key anyway.
func TestProcessorRedisDedupeHonorsCanceledContext(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	p := &Processor{
		cfg: Config{
			DedupeEnabled: true,
			DedupeKey:     "event_id",
			DedupeWindow:  time.Minute,
		},
		primarySink:  &fakeSink{},
		redisClient:  client,
		dedupeSeenAt: make(map[string]time.Time),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dup, err := p.isDuplicate(ctx, "evt-canceled")
	if err == nil {
		t.Fatalf("expected dedupe error for canceled context, got duplicate=%v err=%v", dup, err)
	}
	if dup {
		t.Fatal("expected duplicate=false when the dedupe check fails")
	}
	if mr.Exists("evt-canceled") {
		t.Fatal("dedupe key was written despite a canceled caller context")
	}
}

// TestProcessorRedisDedupeUsesCallerDeadline verifies that a caller deadline
// bounds a Redis dedupe operation against a hung Redis server. Without the
// derived timeout, Process would block until Redis's own (long) read timeout.
func TestProcessorRedisDedupeUsesCallerDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// Accept connections but never reply, simulating a hung Redis server.
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	client := redis.NewClient(&redis.Options{
		Addr:                  ln.Addr().String(),
		DialTimeout:           time.Second,
		ReadTimeout:           30 * time.Second,
		WriteTimeout:          time.Second,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
	})
	defer func() { _ = client.Close() }()

	p := &Processor{
		cfg: Config{
			DedupeEnabled: true,
			DedupeKey:     "event_id",
			DedupeWindow:  time.Minute,
		},
		primarySink:  &fakeSink{},
		redisClient:  client,
		dedupeSeenAt: make(map[string]time.Time),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	res := p.Process(ctx, []byte(`{"event_id":"evt-timeout"}`))
	elapsed := time.Since(start)

	if !errors.Is(res.Err, ErrDedupeUnavailable) {
		t.Fatalf("expected ErrDedupeUnavailable, got %+v", res)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Process blocked for %v despite a 200ms caller deadline", elapsed)
// alwaysFailSink returns an error on every write so that all retry attempts
// (and therefore retry jitter) are exercised on every Process call.
type alwaysFailSink struct {
	err error
}

func (s *alwaysFailSink) Name() string { return "always-fail" }

func (s *alwaysFailSink) WriteEvent(context.Context, []byte, *collectorevent.Event) error {
	return s.err
}
func (s *alwaysFailSink) Flush(context.Context) error { return nil }
func (s *alwaysFailSink) Close(context.Context) error { return nil }

// TestProcessorConcurrentRetryJitter exercises retry jitter from many
// goroutines against a single Processor sharing one *rand.Rand. Under
// `go test -race` this detects the data race on math/rand.Rand's internal
// state that occurs when Process is called concurrently by request handlers.
func TestProcessorConcurrentRetryJitter(t *testing.T) {
	primary := &alwaysFailSink{err: errors.New("primary failed")}
	p, err := New(Config{
		DeliveryPolicy:      "require_primary",
		RetryEnabled:        true,
		RetryMaxAttempts:    3,
		RetryInitialBackoff: time.Microsecond,
		RetryMaxBackoff:     time.Millisecond,
		RetryJitter:         true,
		ValidateJSONObjects: true,
	}, primary, nil, nil, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	const (
		goroutines = 64
		iterations = 32
	)
	raw := []byte(`{"event_id":"evt-jitter","event":"a"}`)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				res := p.Process(context.Background(), raw)
				if res.Err == nil {
					errCh <- errors.New("expected processing error from always-failing sink")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
