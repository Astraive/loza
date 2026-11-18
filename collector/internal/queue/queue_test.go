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
