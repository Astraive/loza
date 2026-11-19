package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	collectorevent "github.com/astraive/loza/collector/internal/event"
	processing "github.com/astraive/loza/collector/internal/processing"
)

func TestLoadWorkerConfigFromArgsExplicitMissingConfigFileFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, err := loadWorkerConfigFromArgs([]string{"-c", missing})
	if err == nil {
		t.Fatal("expected an explicit -c pointing at a missing file to fail")
	}
	if !strings.Contains(err.Error(), "config file") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected an error identifying the missing config file, got: %v", err)
	}
}

type testSink struct {
	mu     sync.Mutex
	writes int
	err    error
}

func (s *testSink) Name() string { return "test" }

func (s *testSink) WriteEvent(context.Context, []byte, *collectorevent.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return s.err
}

func (s *testSink) Flush(context.Context) error { return nil }
func (s *testSink) Close(context.Context) error { return nil }

func TestWatchSignalsExitsWhenContextCanceledElsewhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, exited := watchSignals(ctx, cancel, syscall.SIGINT)
	defer stop()

	// Simulate shutdown triggered by a path other than a delivered signal.
	cancel()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("signal watcher leaked after context was canceled without a signal")
	}
}

func TestWatchSignalsStopReleasesWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, exited := watchSignals(ctx, cancel, syscall.SIGINT)

	// The context is intentionally left uncanceled: only the done channel can
	// release the watcher here. Without it stop() blocks forever.
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not release the signal watcher")
	}
	select {
	case <-exited:
	default:
		t.Fatal("watcher goroutine did not exit")
	}
}

func TestHandleRecordCommitsWhenProcessingFails(t *testing.T) {
	sink := &testSink{err: errors.New("primary failed")}
	processor, err := processing.New(processing.Config{
		DeliveryPolicy:      "require_primary",
		RetryEnabled:        true,
		RetryMaxAttempts:    1,
		RetryInitialBackoff: time.Millisecond,
		RetryMaxBackoff:     time.Millisecond,
		ValidateJSONObjects: true,
	}, sink, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = processor.Close() }()

	state := &workerState{processor: processor}
	committed := 0
	state.handleRecord(context.Background(), queueRecord{
		value: []byte(`{"event":"a"}`),
		commit: func(context.Context) error {
			committed++
			return nil
		},
	})
	if committed != 1 {
		t.Fatalf("expected commit on failure, got %d", committed)
	}
}

func TestHandleRecordCommitsInvalidEvent(t *testing.T) {
	processor, err := processing.New(processing.Config{
		ValidateJSONObjects: true,
	}, &testSink{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = processor.Close() }()

	state := &workerState{processor: processor}
	committed := 0
	state.handleRecord(context.Background(), queueRecord{
		value: []byte(`123`),
		commit: func(context.Context) error {
			committed++
			return nil
		},
	})
	if committed != 1 {
		t.Fatalf("expected commit for invalid record, got %d", committed)
	}
}

type closeCountingReader struct {
	mu      sync.Mutex
	closes  int
	closeFn func() error
}

func (r *closeCountingReader) Poll(context.Context, time.Duration) ([]queueRecord, error) {
	return nil, nil
}

func (r *closeCountingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
	if r.closeFn != nil {
		return r.closeFn()
	}
	return nil
}

func (r *closeCountingReader) closeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closes
}

// Reproduces the shutdown path in runWorker: a deferred Close plus an explicit
// Close. The underlying reader must be closed exactly once even though Close is
// invoked twice (kgo.Client.Close is not idempotent).
func TestCloseOnceReaderClosesUnderlyingReaderOnce(t *testing.T) {
	base := &closeCountingReader{}
	reader := &closeOnceReader{queueReader: base}

	// Simulate the explicit shutdown call followed by the deferred cleanup.
	if err := reader.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got := base.closeCount(); got != 1 {
		t.Fatalf("underlying reader closed %d times, want 1", got)
	}
}

func TestCloseOnceReaderPropagatesFirstErrorWithoutReclosing(t *testing.T) {
	wantErr := errors.New("close failed")
	base := &closeCountingReader{closeFn: func() error { return wantErr }}
	reader := &closeOnceReader{queueReader: base}

	if err := reader.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("first close error = %v, want %v", err, wantErr)
	}
	if err := reader.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("second close error = %v, want %v", err, wantErr)
	}
	if got := base.closeCount(); got != 1 {
		t.Fatalf("underlying reader closed %d times, want 1", got)
type stubQueueReader struct {
	calls int
	poll  func(call int) ([]queueRecord, error)
}

func (r *stubQueueReader) Poll(context.Context, time.Duration) ([]queueRecord, error) {
	r.calls++
	if r.poll == nil {
		return nil, nil
	}
	return r.poll(r.calls)
}

func (r *stubQueueReader) Close() error { return nil }

// TestRunThrottlesPersistentPollErrors reproduces the reported behavior: with
// the old code a permanent Poll error spun thousands of times in this window.
func TestRunThrottlesPersistentPollErrors(t *testing.T) {
	originalSleep := sleepWithContext
	sleepWithContext = sleepWithContextReal
	defer func() { sleepWithContext = originalSleep }()

	reader := &stubQueueReader{poll: func(int) ([]queueRecord, error) {
		return nil, errors.New("broker unavailable")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := (&workerState{}).run(ctx, reader)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run error = %v, want context.DeadlineExceeded", err)
	}
	if reader.calls == 0 {
		t.Fatal("Poll was never called")
	}
	if reader.calls > 10 {
		t.Fatalf("Poll called %d times in 150ms; expected bounded backoff", reader.calls)
	}
}

// TestRunPollErrorBackoffIsBounded checks that consecutive failures back off
// exponentially, capped at workerPollErrorMaxBackoff.
func TestRunPollErrorBackoffIsBounded(t *testing.T) {
	originalSleep := sleepWithContext
	defer func() { sleepWithContext = originalSleep }()

	var waits []time.Duration
	sleepWithContext = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 8 {
			return context.Canceled // simulate shutdown during backoff
		}
		return nil
	}
	reader := &stubQueueReader{poll: func(int) ([]queueRecord, error) {
		return nil, errors.New("persistent failure")
	}}

	err := (&workerState{}).run(context.Background(), reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	// Build the expectation explicitly: double each time, capped at the max.
	want := make([]time.Duration, 0, 8)
	current := workerPollErrorBackoff
	for i := 0; i < 8; i++ {
		want = append(want, current)
		current *= 2
		if current > workerPollErrorMaxBackoff {
			current = workerPollErrorMaxBackoff
		}
	}
	if len(waits) != len(want) {
		t.Fatalf("backoff waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("backoff waits = %v, want %v", waits, want)
		}
	}
	if reader.calls != len(want) {
		t.Fatalf("Poll called %d times, want %d", reader.calls, len(want))
	}
}

// TestRunPollErrorBackoffResetsOnSuccess checks that a successful poll clears
// the consecutive-failure streak so a later isolated error is retried quickly.
func TestRunPollErrorBackoffResetsOnSuccess(t *testing.T) {
	originalSleep := sleepWithContext
	defer func() { sleepWithContext = originalSleep }()

	var waits []time.Duration
	sleepWithContext = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 4 {
			return context.Canceled
		}
		return nil
	}
	reader := &stubQueueReader{poll: func(call int) ([]queueRecord, error) {
		if call == 3 {
			return nil, nil // successful (empty) poll
		}
		return nil, errors.New("transient failure")
	}}

	err := (&workerState{}).run(context.Background(), reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	want := []time.Duration{
		workerPollErrorBackoff,
		2 * workerPollErrorBackoff,
		workerPollErrorBackoff,
		2 * workerPollErrorBackoff,
	}
	if len(waits) != len(want) {
		t.Fatalf("backoff waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("backoff waits = %v, want %v", waits, want)
		}
	}
}

func TestLoadWorkerConfigFromArgsQueueOverrides(t *testing.T) {
	t.Setenv("COLLECTOR_KAFKA_BROKERS", "k1:9092,k2:9092")
	t.Setenv("COLLECTOR_KAFKA_TOPIC", "topic-a")
	t.Setenv("LOZA_WORKER_CONSUMER_GROUP", "group-a")
	t.Setenv("LOZA_WORKER_POLL_TIMEOUT", "4s")

	cfg, err := loadWorkerConfigFromArgs(nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.kafkaBrokers) != 2 || cfg.kafkaBrokers[0] != "k1:9092" {
		t.Fatalf("unexpected brokers: %#v", cfg.kafkaBrokers)
	}
	if cfg.kafkaTopic != "topic-a" {
		t.Fatalf("unexpected topic: %q", cfg.kafkaTopic)
	}
	if cfg.workerConsumerGroup != "group-a" {
		t.Fatalf("unexpected consumer group: %q", cfg.workerConsumerGroup)
	}
	if cfg.workerPollTimeout != 4*time.Second {
		t.Fatalf("unexpected poll timeout: %s", cfg.workerPollTimeout)
	}
}
