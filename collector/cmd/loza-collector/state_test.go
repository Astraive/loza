package main

import "testing"

// TestEffectiveDiskHealthyDiskFullWithoutSinkErrors reproduces MASS-0127: a
// disk-full signal (mirroring the OnDiskFull callback, which only flips
// diskHealthy to false) must be reported as unhealthy even when no sink/DLQ
// write error was recorded. Before the fix, effectiveDiskHealthy fell back to
// "sinkWriteErrors == 0" and reported a full disk as healthy, keeping
// isReady() and the readiness metric green.
func TestEffectiveDiskHealthyDiskFullWithoutSinkErrors(t *testing.T) {
	state := &collectorState{cfg: testCollectorConfig()}
	state.ready.Store(true)
	state.sinkHealthy.Store(true)
	state.spoolHealthy.Store(true)
	state.diskHealthy.Store(true)

	// Healthy baseline: disk is fine and there are no sink write errors.
	if !state.effectiveDiskHealthy() {
		t.Fatal("expected effectiveDiskHealthy to be true before disk-full signal")
	}
	if !state.isReady() {
		t.Fatal("expected isReady to be true before disk-full signal")
	}

	// Simulate OnDiskFull: only diskHealthy flips, sinkWriteErrors stays at 0.
	state.diskHealthy.Store(false)
	if state.metrics.sinkWriteErrors.Load() != 0 {
		t.Fatalf("test precondition violated: sinkWriteErrors = %d, want 0", state.metrics.sinkWriteErrors.Load())
	}

	if state.effectiveDiskHealthy() {
		t.Fatal("expected effectiveDiskHealthy to be false after disk-full signal with no sink write errors")
	}
	if state.isReady() {
		t.Fatal("expected isReady to be false after disk-full signal with no sink write errors")
	}
}

// TestEffectiveSinkHealthyStillUsesSinkErrors guards the adjacent sink-health
// path: sink write errors must still drive sink health, and disk health must
// depend only on diskHealthy rather than on the shared error counter.
func TestEffectiveSinkHealthyStillUsesSinkErrors(t *testing.T) {
	state := &collectorState{cfg: testCollectorConfig()}
	state.sinkHealthy.Store(false)
	state.diskHealthy.Store(true)

	state.metrics.sinkWriteErrors.Add(1)
	if state.effectiveSinkHealthy() {
		t.Fatal("expected effectiveSinkHealthy to be false when sinkWriteErrors > 0")
	}
	// A sink write error alone must not flip disk health.
	if !state.effectiveDiskHealthy() {
		t.Fatal("expected effectiveDiskHealthy to remain true when only sinkWriteErrors > 0")
	}
}
