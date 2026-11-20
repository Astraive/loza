package queue

import (
	"context"
	"encoding/base64"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sizeLimitedSink is an in-memory Sink used to exercise queue backpressure
// without any filesystem or network dependency.
type sizeLimitedSink struct {
	mu      sync.Mutex
	batches [][]byte
}

func (s *sizeLimitedSink) WriteEvent(ctx context.Context, data []byte, key []byte) error {
	return s.WriteBatch(ctx, [][]byte{data})
}

func (s *sizeLimitedSink) WriteBatch(ctx context.Context, events [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, events...)
	return nil
}

func (s *sizeLimitedSink) Flush(ctx context.Context) error { return nil }

func (s *sizeLimitedSink) Close(ctx context.Context) error { return nil }

// newSizeLimitedQueue builds a queue whose only limit is a byte cap: MaxEvents
// is left at 0 (unbounded) so the byte cap is the sole backpressure mechanism.
// The flush loop is effectively disabled so buffered sizes stay deterministic.
func newSizeLimitedQueue(t *testing.T, maxSizeBytes int64) (*Queue, *sizeLimitedSink) {
	t.Helper()
	sink := &sizeLimitedSink{}
	q, err := NewQueue(QueueConfig{
		Directory:     filepath.Join(t.TempDir(), "queue"),
		Sink:          sink,
		MaxEvents:     0,
		MaxSizeBytes:  maxSizeBytes,
		BatchSize:     100,
		BatchTimeout:  time.Hour,
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	// Signal shutdown without Queue.Close(): the flush loop may be parked in
	// cond.Wait() and Close does not broadcast after closing stopCh.
	t.Cleanup(func() {
		close(q.stopCh)
		q.cond.Broadcast()
	})
	return q, sink
}

func bufferedState(q *Queue) (int, int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events), q.queueBytes
}

func TestMaxSizeBytesBackpressureEnqueue(t *testing.T) {
	q, _ := newSizeLimitedQueue(t, 10)

	// Four-byte payloads: the 10-byte cap admits exactly two events.
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("abcd")}); err != nil {
		t.Fatalf("enqueue 0: unexpected error: %v", err)
	}
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("efgh")}); err != nil {
		t.Fatalf("enqueue 1: unexpected error: %v", err)
	}
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("ijkl")}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("enqueue 2: expected ErrQueueFull, got %v", err)
	}

	n, bytes := bufferedState(q)
	if n != 2 || bytes != 8 {
		t.Fatalf("buffered = (%d events, %d bytes), want (2, 8)", n, bytes)
	}
	if got := q.State().Backpressured; got != 1 {
		t.Fatalf("backpressured = %d, want 1", got)
	}
}

func TestMaxSizeBytesBackpressureEnqueueBatch(t *testing.T) {
	q, _ := newSizeLimitedQueue(t, 10)

	errs := q.EnqueueBatch(context.Background(), []Event{
		{Payload: []byte("abcd")},
		{Payload: []byte("efgh")},
		{Payload: []byte("ijkl")}, // would push the total to 12 bytes
	})
	if len(errs) != 3 {
		t.Fatalf("got %d errors, want 3", len(errs))
	}
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("first two events should be enqueued, got %v", errs[:2])
	}
	if !errors.Is(errs[2], ErrQueueFull) {
		t.Fatalf("third event: expected ErrQueueFull, got %v", errs[2])
	}

	n, bytes := bufferedState(q)
	if n != 2 || bytes != 8 {
		t.Fatalf("buffered = (%d events, %d bytes), want (2, 8)", n, bytes)
	}
	if got := q.State().Backpressured; got != 1 {
		t.Fatalf("backpressured = %d, want 1", got)
	}
}

func TestMaxSizeBytesBackpressureReleasedOnFlush(t *testing.T) {
	q, _ := newSizeLimitedQueue(t, 4)

	// A single event larger than the whole cap is rejected even when empty.
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("too-large")}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("oversized event: expected ErrQueueFull, got %v", err)
	}
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("abcd")}); err != nil {
		t.Fatalf("enqueue within limit: %v", err)
	}
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("efgh")}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second event: expected ErrQueueFull, got %v", err)
	}

	// Flushing drains the buffer and must release the accounted bytes.
	if err := q.doFlush(); err != nil {
		t.Fatalf("doFlush: %v", err)
	}
	if n, bytes := bufferedState(q); n != 0 || bytes != 0 {
		t.Fatalf("after flush buffered = (%d events, %d bytes), want (0, 0)", n, bytes)
	}

	if err := q.Enqueue(context.Background(), Event{Payload: []byte("wxyz")}); err != nil {
		t.Fatalf("enqueue after flush: %v", err)
	}
	if n, bytes := bufferedState(q); n != 1 || bytes != 4 {
		t.Fatalf("after re-enqueue buffered = (%d events, %d bytes), want (1, 4)", n, bytes)
	}
}

func TestDiskSinkEncryptsPayloadAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.ndjson")
	sink, err := NewDiskSink(DiskSinkConfig{
		Path:       path,
		EncryptKey: "queue-secret",
	})
	if err != nil {
		t.Fatalf("new disk sink: %v", err)
	}
	defer func() {
		_ = sink.Close(context.Background())
	}()

	payload := []byte(`{"event_id":"evt-1","message":"secret"}`)
	if err := sink.WriteEvent(context.Background(), payload, nil); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sink file: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("expected persisted queue payload")
	}
	if strings.Contains(string(raw), `"message":"secret"`) || strings.Contains(string(raw), `"event_id":"evt-1"`) {
		t.Fatalf("expected encrypted payload, got plaintext %q", string(raw))
	}
}

func TestDiskSinkRotationRetainsMaxFilesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.ndjson")
	const maxFiles = 3
	sink, err := NewDiskSink(DiskSinkConfig{
		Path:        path,
		MaxFileSize: 1, // force a rotation on every write after the first
		MaxFiles:    maxFiles,
	})
	if err != nil {
		t.Fatalf("new disk sink: %v", err)
	}
	defer func() {
		_ = sink.Close(context.Background())
	}()

	ctx := context.Background()
	for _, marker := range []string{"gen-a", "gen-b", "gen-c", "gen-d", "gen-e"} {
		if err := sink.WriteEvent(ctx, []byte(marker), nil); err != nil {
			t.Fatalf("write event %s: %v", marker, err)
		}
	}

	// After the final write the active file holds "gen-e". The retained
	// backups are .0 (gen-d), .1 (gen-c) and .2 (gen-b); the oldest event,
	// "gen-a", is discarded once more than MaxFiles generations exist.
	want := map[string]string{
		".0": "gen-d",
		".1": "gen-c",
		".2": "gen-b",
	}
	for suffix, marker := range want {
		backup := filepath.Join(dir, "queue"+suffix+".ndjson")
		raw, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("read backup %s: %v", backup, err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(marker))
		if !strings.Contains(string(raw), encoded) {
			t.Fatalf("backup %s = %q, want it to contain payload %q", backup, raw, encoded)
		}
	}

	// No backup beyond MaxFiles should ever be created.
	extra := filepath.Join(dir, "queue.3.ndjson")
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup beyond MaxFiles: %s (err=%v)", extra, err)
	}

	// The active file still holds the newest event.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active file: %v", err)
	}
	if encoded := base64.StdEncoding.EncodeToString([]byte("gen-e")); !strings.Contains(string(raw), encoded) {
		t.Fatalf("active file = %q, want it to contain payload %q", raw, encoded)
	}
}

// countingSink records every WriteBatch invocation so tests can assert when
// delivery attempts happen.
type countingSink struct {
	mu    sync.Mutex
	calls []time.Time
	fail  bool
}

func (s *countingSink) WriteEvent(ctx context.Context, data []byte, key []byte) error {
	return s.WriteBatch(ctx, [][]byte{data})
}

func (s *countingSink) WriteBatch(ctx context.Context, events [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, time.Now())
	if s.fail {
		return errors.New("sink unavailable")
	}
	return nil
}

type recordingSink struct {
	mu      sync.Mutex
	fail    bool
	writes  chan [][]byte
	batches int
}

func newTestQueue(t *testing.T, cfg QueueConfig) (*Queue, *recordingSink) {
	t.Helper()
	sink := &recordingSink{writes: make(chan [][]byte, 8)}
	cfg.Directory = t.TempDir()
	cfg.Sink = sink
	q, err := NewQueue(cfg)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return q, sink
}

func (s *recordingSink) setFail(fail bool) {
	s.mu.Lock()
	s.fail = fail
	s.mu.Unlock()
}

func (s *recordingSink) WriteEvent(ctx context.Context, data []byte, key []byte) error {
	return s.WriteBatch(ctx, [][]byte{data})
}

func (s *recordingSink) WriteBatch(ctx context.Context, events [][]byte) error {
	s.mu.Lock()
	s.batches++
	fail := s.fail
	s.mu.Unlock()
	cp := make([][]byte, len(events))
	copy(cp, events)
	select {
	case s.writes <- cp:
	default:
	}
	if fail {
		return errors.New("sink unavailable")
	}
	return nil
}

func (s *countingSink) Flush(ctx context.Context) error { return nil }
func (s *countingSink) Close(ctx context.Context) error { return nil }

func (s *countingSink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *countingSink) callTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Time, len(s.calls))
	copy(out, s.calls)
	return out
}

func waitForSinkCalls(t *testing.T, s *countingSink, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d sink calls; got %d", want, s.callCount())
}

func TestRetryBackoffExponentialCapAndJitter(t *testing.T) {
	q := &Queue{
		cfg: QueueConfig{
			RetryInitialBackoff: 100 * time.Millisecond,
			RetryMaxBackoff:     350 * time.Millisecond,
		},
		rng: rand.New(rand.NewSource(1)),
	}

	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 350 * time.Millisecond}, // capped by RetryMaxBackoff
		{4, 350 * time.Millisecond},
	} {
		if got := q.retryBackoff(tc.attempt); got != tc.want {
			t.Errorf("attempt %d: got backoff %v, want %v", tc.attempt, got, tc.want)
		}
	}

	jq := &Queue{
		cfg: QueueConfig{
			RetryInitialBackoff: time.Second,
			RetryMaxBackoff:     time.Second,
			RetryJitter:         true,
		},
		rng: rand.New(rand.NewSource(2)),
	}
	for i := 0; i < 100; i++ {
		got := jq.retryBackoff(1)
		if got < 800*time.Millisecond || got >= 1200*time.Millisecond {
			t.Fatalf("jittered backoff %v outside [800ms, 1200ms)", got)
		}
	}
}

// TestFailedFlushDefersRetryUntilBackoff drives doFlush directly to verify
// that a failed event is not re-enqueued for immediate delivery: it carries a
// future deadline, is skipped by the next flush, and is retried only after its
// configured backoff has elapsed.
func TestFailedFlushDefersRetryUntilBackoff(t *testing.T) {
	sink := &countingSink{fail: true}
	q := &Queue{
		cfg: QueueConfig{
			Sink:                sink,
			BatchSize:           100,
			RetryEnabled:        true,
			RetryMaxAttempts:    3,
			RetryInitialBackoff: 120 * time.Millisecond,
			RetryMaxBackoff:     time.Second,
		},
		events:   []Event{{ID: "evt-1", Payload: []byte("payload")}},
		inflight: map[string]Event{"evt-1": {ID: "evt-1"}},
		stopCh:   make(chan struct{}),
		rng:      rand.New(rand.NewSource(3)),
	}
	q.cond = sync.NewCond(&q.mu)

	if err := q.doFlush(); err == nil {
		t.Fatal("expected write failure from doFlush")
	}
	if got := sink.callCount(); got != 1 {
		t.Fatalf("expected 1 delivery attempt, got %d", got)
	}

	q.mu.Lock()
	if len(q.events) != 1 {
		t.Fatalf("expected 1 requeued retry event, got %d", len(q.events))
	}
	deadline := q.events[0].nextAttempt
	q.mu.Unlock()
	if deadline.IsZero() || !deadline.After(time.Now()) {
		t.Fatalf("retry event missing future backoff deadline: %v", deadline)
	}

	// A flush before the deadline must not attempt delivery again.
	if err := q.doFlush(); err != nil {
		t.Fatalf("unexpected flush error while deferred: %v", err)
	}
	if got := sink.callCount(); got != 1 {
		t.Fatalf("premature retry: expected 1 delivery attempt, got %d", got)
	}

	// Once the backoff has elapsed the retry becomes eligible.
	time.Sleep(time.Until(deadline) + 15*time.Millisecond)
	if err := q.doFlush(); err == nil {
		t.Fatal("expected retry attempt to fail again")
	}
	if got := sink.callCount(); got != 2 {
		t.Fatalf("expected retry after backoff (2 attempts), got %d", got)
	}
}

// TestQueueHonorsBackoffBeforeAutomaticRetry exercises the full flush loop:
// with a failing sink the second delivery attempt must wait out the
// configured backoff rather than firing immediately after the first failure.
func TestQueueHonorsBackoffBeforeAutomaticRetry(t *testing.T) {
	const backoff = 300 * time.Millisecond
	sink := &countingSink{fail: true}
	q, err := NewQueue(QueueConfig{
		Directory:           t.TempDir(),
		Sink:                sink,
		BatchSize:           1, // flush the first event immediately on enqueue
		FlushInterval:       time.Hour,
		RetryEnabled:        true,
		RetryMaxAttempts:    5,
		RetryInitialBackoff: backoff,
		RetryMaxBackoff:     time.Second,
	})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	defer close(q.stopCh) // stop the background loop at test end without blocking in cond.Wait

	if err := q.Enqueue(context.Background(), Event{ID: "evt-1", Payload: []byte("payload")}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	waitForSinkCalls(t, sink, 1, 3*time.Second)

	// Well before the backoff expires there must be no second attempt.
	time.Sleep(120 * time.Millisecond)
	if got := sink.callCount(); got != 1 {
		t.Fatalf("retry occurred before configured backoff: got %d attempts", got)
	}

	waitForSinkCalls(t, sink, 2, 3*time.Second)
	calls := sink.callTimes()
	if gap := calls[1].Sub(calls[0]); gap < 200*time.Millisecond {
		t.Fatalf("retry gap %v shorter than configured backoff %v", gap, backoff)
	}
}

func (s *recordingSink) Flush(context.Context) error { return nil }
func (s *recordingSink) Close(context.Context) error { return nil }

func awaitBatch(t *testing.T, sink *recordingSink) [][]byte {
	t.Helper()
	select {
	case batch := <-sink.writes:
		return batch
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a sink batch")
		return nil
	}
}

func waitForCircuit(t *testing.T, q *Queue, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for q.State().CircuitOpen != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if q.State().CircuitOpen != want {
		t.Fatalf("expected circuit open=%t, got %t", want, q.State().CircuitOpen)
	}
}

// Regression: BatchTimeout was declared and defaulted but never read. A
// partial batch must be flushed once its oldest event has waited that long,
// without waiting for FlushInterval or a full BatchSize.
func TestBatchTimeoutFlushesPartialBatch(t *testing.T) {
	q, sink := newTestQueue(t, QueueConfig{
		BatchSize:     100,
		BatchTimeout:  40 * time.Millisecond,
		FlushInterval: time.Hour,
	})
	if err := q.Enqueue(context.Background(), Event{ID: "evt-timeout", Payload: []byte("hello")}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	batch := awaitBatch(t, sink)
	if len(batch) != 1 || string(batch[0]) != "hello" {
		t.Fatalf("unexpected batch: %q", batch)
	}
	if st := q.State(); st.Flushed != 1 {
		t.Fatalf("expected 1 flushed event, got %d", st.Flushed)
	}
}

func TestBatchSizeFlushStillWorks(t *testing.T) {
	q, sink := newTestQueue(t, QueueConfig{
		BatchSize:     3,
		BatchTimeout:  time.Hour,
		FlushInterval: time.Hour,
	})
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(context.Background(), Event{Payload: []byte("x")}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if batch := awaitBatch(t, sink); len(batch) != 3 {
		t.Fatalf("expected a batch of 3, got %d", len(batch))
	}
}

func TestFlushIntervalFlushStillWorks(t *testing.T) {
	q, sink := newTestQueue(t, QueueConfig{
		BatchSize:     100,
		BatchTimeout:  time.Hour,
		FlushInterval: 40 * time.Millisecond,
	})
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("interval")}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if batch := awaitBatch(t, sink); len(batch) != 1 || string(batch[0]) != "interval" {
		t.Fatalf("unexpected batch: %q", batch)
	}
}

// Regression: CircuitBreakerHalfOpen was declared but never read. After the
// cooldown, a single trial request must be admitted; its outcome closes or
// reopens the circuit.
func TestCircuitBreakerHalfOpenClosesOnSuccess(t *testing.T) {
	q, _ := newTestQueue(t, QueueConfig{
		CircuitBreakerThreshold: 1,
		CircuitBreakerTimeout:   time.Hour,
		CircuitBreakerHalfOpen:  true,
	})
	q.recordFailure()
	if !q.isCircuitOpen() {
		t.Fatal("expected circuit to be open after failure")
	}
	q.circuitMu.Lock()
	q.circuitOpenTime = time.Now().Add(-2 * time.Hour)
	q.circuitMu.Unlock()

	if q.isCircuitOpen() {
		t.Fatal("expected one trial request to be admitted in half-open state")
	}
	if !q.halfOpenTrial {
		t.Fatal("expected a half-open trial to be in flight")
	}
	if !q.isCircuitOpen() {
		t.Fatal("expected further requests to be rejected while the trial is in flight")
	}

	q.recordSuccess()
	if q.isCircuitOpen() {
		t.Fatal("expected circuit to close after a successful trial")
	}
	if q.halfOpenTrial || q.State().CircuitOpen {
		t.Fatal("expected half-open trial cleared and circuit state closed")
	}
}

func TestCircuitBreakerHalfOpenReopensOnFailure(t *testing.T) {
	q, _ := newTestQueue(t, QueueConfig{
		CircuitBreakerThreshold: 1,
		CircuitBreakerTimeout:   time.Hour,
		CircuitBreakerHalfOpen:  true,
	})
	q.recordFailure()
	q.circuitMu.Lock()
	q.circuitOpenTime = time.Now().Add(-2 * time.Hour)
	q.circuitMu.Unlock()

	if q.isCircuitOpen() {
		t.Fatal("expected one trial request to be admitted in half-open state")
	}
	q.recordFailure()
	if !q.isCircuitOpen() {
		t.Fatal("expected circuit to reopen after a failed trial")
	}
	if q.halfOpenTrial || !q.State().CircuitOpen {
		t.Fatal("expected half-open trial cleared and circuit state open")
	}
}

func TestCircuitBreakerWithoutHalfOpenStillAutoCloses(t *testing.T) {
	q, _ := newTestQueue(t, QueueConfig{
		CircuitBreakerThreshold: 1,
		CircuitBreakerTimeout:   time.Hour,
		CircuitBreakerHalfOpen:  false,
	})
	q.recordFailure()
	if !q.isCircuitOpen() {
		t.Fatal("expected circuit to be open after failure")
	}
	q.circuitMu.Lock()
	q.circuitOpenTime = time.Now().Add(-2 * time.Hour)
	q.circuitMu.Unlock()

	if q.isCircuitOpen() {
		t.Fatal("expected circuit to auto-close after timeout when half-open is disabled")
	}
	if q.halfOpenTrial || q.State().CircuitOpen {
		t.Fatal("expected no trial and a closed circuit state")
	}
}

// End-to-end check that both config fields are honored through the public
// Enqueue path: a failed batch opens the circuit, the cooldown admits exactly
// one half-open trial, and a successful trial closes the circuit again.
func TestCircuitBreakerHalfOpenEndToEnd(t *testing.T) {
	q, sink := newTestQueue(t, QueueConfig{
		BatchSize:               100,
		BatchTimeout:            60 * time.Millisecond,
		FlushInterval:           time.Hour,
		CircuitBreakerThreshold: 1,
		CircuitBreakerTimeout:   10 * time.Millisecond,
		CircuitBreakerHalfOpen:  true,
	})

	sink.setFail(true)
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("failing")}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	awaitBatch(t, sink) // failing WriteBatch opens the circuit
	waitForCircuit(t, q, true)

	time.Sleep(30 * time.Millisecond) // let the cooldown elapse
	sink.setFail(false)

	if err := q.Enqueue(context.Background(), Event{Payload: []byte("trial")}); err != nil {
		t.Fatalf("half-open trial enqueue was rejected: %v", err)
	}
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("blocked")}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected second enqueue to be rejected while trial in flight, got %v", err)
	}

	awaitBatch(t, sink) // successful trial closes the circuit
	waitForCircuit(t, q, false)
	if err := q.Enqueue(context.Background(), Event{Payload: []byte("recovered")}); err != nil {
		t.Fatalf("enqueue after recovery was rejected: %v", err)
	}
}

type stubSink struct {
	closeErr error
}

func (*stubSink) WriteEvent(context.Context, []byte, []byte) error { return nil }
func (*stubSink) WriteBatch(context.Context, [][]byte) error       { return nil }
func (*stubSink) Flush(context.Context) error                      { return nil }
func (s *stubSink) Close(context.Context) error                    { return s.closeErr }

func newQueueWithSink(t *testing.T, sink Sink) *Queue {
	t.Helper()
	q, err := NewQueue(QueueConfig{Directory: t.TempDir(), Sink: sink})
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	return q
}

// TestQueueCloseIsIdempotent reproduces the reported defect: a second Close()
// used to panic with "close of closed channel". It also exercises the case
// where the flush loop is parked in cond.Wait() when shutdown starts.
func TestQueueCloseIsIdempotent(t *testing.T) {
	q := newQueueWithSink(t, &stubSink{})
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(context.Background(), Event{Payload: []byte("event")}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	// Give flushLoop time to park in cond.Wait() before shutdown begins.
	time.Sleep(5 * time.Millisecond)

	if err := q.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	if err := q.Enqueue(context.Background(), Event{Payload: []byte("late")}); err != ErrQueueClosed {
		t.Fatalf("enqueue after close: got %v, want ErrQueueClosed", err)
	}
}

// TestQueueCloseConcurrentReturnsStoredError verifies that concurrent Close()
// calls neither panic nor race, and that every caller receives the same stored
// error from the single shutdown performed by sync.Once.
func TestQueueCloseConcurrentReturnsStoredError(t *testing.T) {
	wantErr := errors.New("sink close failed")
	q := newQueueWithSink(t, &stubSink{closeErr: wantErr})
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(context.Background(), Event{Payload: []byte("event")}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	time.Sleep(5 * time.Millisecond)

	const callers = 8
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = q.Close()
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, wantErr) {
			t.Errorf("caller %d: got %v, want %v", i, err, wantErr)
		}
	}
}

// TestQueueCloseFlushesRemainingEvents guards against regressing the final
// flush performed during shutdown.
func TestQueueCloseFlushesRemainingEvents(t *testing.T) {
	q := newQueueWithSink(t, &stubSink{})
	const events = 3
	for i := 0; i < events; i++ {
		if err := q.Enqueue(context.Background(), Event{Payload: []byte("event")}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := q.State().Flushed; got != events {
		t.Fatalf("flushed %d events, want %d", got, events)
	}
}
