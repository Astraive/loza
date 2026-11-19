package main

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// assertExclusiveIngestCapacity releases a burst of concurrent ingests against
// a state whose configured limit allows exactly one. The capacity check and
// reservation happen in acquireIngestCapacity; if they are not atomic, more
// than one caller observes a free slot and reserves it.
func assertExclusiveIngestCapacity(t *testing.T, cfg collectorConfig) {
	t.Helper()
	state := &collectorState{cfg: cfg}

	const (
		goroutines = 256
		iterations = 200
	)

	for iter := 0; iter < iterations; iter++ {
		var gate atomic.Bool
		var wg sync.WaitGroup
		var acquired atomic.Int64
		releases := make([]func(), goroutines)

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for !gate.Load() {
					runtime.Gosched()
				}
				gerr, release := state.acquireIngestCapacity(1, 1)
				if gerr == nil {
					acquired.Add(1)
					releases[i] = release
				}
			}(i)
		}
		gate.Store(true)
		wg.Wait()

		if got := acquired.Load(); got > 1 {
			t.Fatalf("iteration %d: %d callers acquired capacity simultaneously; limit is 1", iter, got)
		}
		if got := state.metrics.inflightRequests.Load(); got != acquired.Load() {
			t.Fatalf("iteration %d: inflightRequests=%d, acquired=%d", iter, got, acquired.Load())
		}
		if got := state.metrics.inflightEvents.Load(); got != acquired.Load() {
			t.Fatalf("iteration %d: inflightEvents=%d, acquired=%d", iter, got, acquired.Load())
		}

		for _, release := range releases {
			if release != nil {
				release()
			}
		}
		if got := state.metrics.inflightRequests.Load(); got != 0 {
			t.Fatalf("iteration %d: inflightRequests=%d after release, want 0", iter, got)
		}
		if got := state.metrics.inflightEvents.Load(); got != 0 {
			t.Fatalf("iteration %d: inflightEvents=%d after release, want 0", iter, got)
		}
	}
}

func TestAcquireIngestCapacityConcurrentRequests(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.maxInflightRequests = 1
	cfg.maxInflightEvents = 0 // isolate the request limit
	cfg.maxQueueBytes = 0
	assertExclusiveIngestCapacity(t, cfg)
}

func TestAcquireIngestCapacityConcurrentEvents(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.maxInflightRequests = 0 // isolate the event limit
	cfg.maxInflightEvents = 1
	cfg.maxQueueBytes = 0
	assertExclusiveIngestCapacity(t, cfg)
	"testing"
)

// limitStateWithoutMemoryLimiter returns a collectorState whose limits.* values
// are configured but whose component registry deliberately omits the
// memory_limiter processor. Configured limits must still be enforced in this
// configuration (regression for MASS-0113 / issue #480).
func limitStateWithoutMemoryLimiter() *collectorState {
	cfg := testCollectorConfig()
	// Explicitly drop memory_limiter so processorEnabled("memory_limiter") is false.
	cfg.processorRegistry = []string{"validate", "size_limit"}
	return &collectorState{cfg: cfg}
}

func TestAcquireIngestCapacityWithoutMemoryLimiterProcessor(t *testing.T) {
	t.Run("max_inflight_requests is enforced", func(t *testing.T) {
		state := limitStateWithoutMemoryLimiter()
		state.cfg.maxInflightRequests = 1
		state.metrics.inflightRequests.Store(1)

		gerr, release := state.acquireIngestCapacity(1, 0)
		if gerr == nil {
			t.Fatal("expected backpressure when max_inflight_requests is configured without memory_limiter")
		}
		if gerr.Code != "backpressure" {
			t.Fatalf("expected backpressure code, got %q", gerr.Code)
		}
		if release != nil {
			t.Fatal("expected no release function on rejection")
		}
	})

	t.Run("max_inflight_events is enforced", func(t *testing.T) {
		state := limitStateWithoutMemoryLimiter()
		state.cfg.maxInflightEvents = 5
		state.metrics.inflightEvents.Store(5)

		gerr, release := state.acquireIngestCapacity(1, 0)
		if gerr == nil {
			t.Fatal("expected backpressure when max_inflight_events is configured without memory_limiter")
		}
		if gerr.Code != "backpressure" {
			t.Fatalf("expected backpressure code, got %q", gerr.Code)
		}
		if release != nil {
			t.Fatal("expected no release function on rejection")
		}
	})

	t.Run("max_queue_bytes is enforced", func(t *testing.T) {
		state := limitStateWithoutMemoryLimiter()
		state.cfg.maxQueueBytes = 10
		state.metrics.queueBytes.Store(10)

		gerr, release := state.acquireIngestCapacity(1, 1)
		if gerr == nil {
			t.Fatal("expected queue_full when max_queue_bytes is configured without memory_limiter")
		}
		if gerr.Code != "queue_full" {
			t.Fatalf("expected queue_full code, got %q", gerr.Code)
		}
		if release != nil {
			t.Fatal("expected no release function on rejection")
		}
	})

	t.Run("under limit acquires and releases capacity", func(t *testing.T) {
		state := limitStateWithoutMemoryLimiter()
		state.cfg.maxInflightRequests = 2
		state.cfg.maxInflightEvents = 10
		state.cfg.maxQueueBytes = 100

		gerr, release := state.acquireIngestCapacity(3, 20)
		if gerr != nil {
			t.Fatalf("unexpected rejection below configured limits: %+v", gerr)
		}
		if release == nil {
			t.Fatal("expected a release function on successful acquisition")
		}
		if got := state.metrics.inflightRequests.Load(); got != 1 {
			t.Fatalf("expected inflight requests 1, got %d", got)
		}
		if got := state.metrics.inflightEvents.Load(); got != 3 {
			t.Fatalf("expected inflight events 3, got %d", got)
		}
		release()
		if got := state.metrics.inflightRequests.Load(); got != 0 {
			t.Fatalf("expected inflight requests 0 after release, got %d", got)
		}
		if got := state.metrics.inflightEvents.Load(); got != 0 {
			t.Fatalf("expected inflight events 0 after release, got %d", got)
		}
	})

	t.Run("zero value still disables a limit", func(t *testing.T) {
		state := limitStateWithoutMemoryLimiter()
		state.cfg.maxInflightRequests = 0
		state.cfg.maxInflightEvents = 0
		state.cfg.maxQueueBytes = 0

		gerr, release := state.acquireIngestCapacity(1, 1)
		if gerr != nil {
			t.Fatalf("zero-valued limits should be disabled: %+v", gerr)
		}
		if release == nil {
			t.Fatal("expected a release function when limits are disabled")
		}
		release()
	})
}
