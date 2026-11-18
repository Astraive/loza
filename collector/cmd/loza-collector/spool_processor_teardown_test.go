package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestMaybeWriteDLQAfterTeardownDoesNotResurrectProcessor verifies that once
// reliability teardown has closed the processor, a concurrent DLQ write can
// neither recreate the processor nor append to the already-closed DLQ file.
func TestMaybeWriteDLQAfterTeardownDoesNotResurrectProcessor(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.dlqEnabled = true
	cfg.dlqPath = filepath.Join(t.TempDir(), "dlq.ndjson")

	state := &collectorState{
		cfg:          cfg,
		ingestSink:   &fakeSink{},
		rng:          randSourceForTests(),
		dedupeSeenAt: make(map[string]time.Time),
	}
	if err := state.ensureProcessor(); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}

	state.closeReliability()

	if !state.processorClosed {
		t.Fatal("expected processorClosed to be set after teardown")
	}
	if state.processor != nil {
		t.Fatal("expected processor to be nil after teardown")
	}

	state.maybeWriteDLQ([]byte(`{"event":"after-close"}`), errors.New("boom"))

	if state.processor != nil {
		t.Fatal("maybeWriteDLQ resurrected the processor after teardown")
	}
	if err := state.ensureProcessor(); !errors.Is(err, errProcessorClosed) {
		t.Fatalf("ensureProcessor after teardown: got %v, want errProcessorClosed", err)
	}

	raw, err := os.ReadFile(cfg.dlqPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read dlq: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "" {
		t.Fatalf("unexpected DLQ write after teardown: %q", got)
	}
}

// TestMaybeWriteDLQConcurrentWithTeardown stresses the DLQ/teardown path.
// Under -race the original unsynchronized close produced a data race and could
// dereference a nil processor; the fix keeps both operations mutually
// exclusive via processorMu.
func TestMaybeWriteDLQConcurrentWithTeardown(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.dlqEnabled = true
	cfg.dlqPath = filepath.Join(t.TempDir(), "dlq.ndjson")

	state := &collectorState{
		cfg:          cfg,
		ingestSink:   &fakeSink{},
		rateLimiter:  rate.NewLimiter(rate.Limit(1000), 1000),
		rng:          randSourceForTests(),
		dedupeSeenAt: make(map[string]time.Time),
	}
	if err := state.ensureProcessor(); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}

	const writers = 8
	const writesPerWriter = 200
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < writesPerWriter; j++ {
				state.maybeWriteDLQ([]byte(`{"event":"race"}`), errors.New("boom"))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		state.closeReliability()
	}()
	close(start)
	wg.Wait()
}
