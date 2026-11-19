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
}
