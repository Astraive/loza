package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// gatedClock blocks inside Now() while its gate is set. Each blocked call
// signals arrived once, letting a test widen the window between the
// MaxCheckpoints count read and the append performed by Checkpoint.
type gatedClock struct {
	mu      sync.Mutex
	now     time.Time
	gate    chan struct{}
	arrived chan struct{}
}

func (c *gatedClock) Now() time.Time {
	c.mu.Lock()
	gate, arrived := c.gate, c.arrived
	c.mu.Unlock()
	if gate != nil {
		arrived <- struct{}{}
		<-gate
	}
	return c.now
}

func (c *gatedClock) setGate(gate, arrived chan struct{}) {
	c.mu.Lock()
	c.gate, c.arrived = gate, arrived
	c.mu.Unlock()
}

// TestCheckpointConcurrentRespectsMaxCheckpoints reproduces the TOCTOU race
// where concurrent Checkpoint calls all observe count < MaxCheckpoints and
// each append, pushing the event past the configured limit.
func TestCheckpointConcurrentRespectsMaxCheckpoints(t *testing.T) {
	const (
		maxCheckpoints = 4
		workers        = 12
	)

	clock := &gatedClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	cfg := Test()
	cfg.Sinks = []Sink{}
	cfg.Clock = clock
	cfg.Checkpoints.Enabled = true
	cfg.Checkpoints.MaxCheckpoints = maxCheckpoints
	cfg.Checkpoints.EmitImmediately = false

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	ctx := l.StartEvent(context.Background(), Params{Event: "checkpoint.race", Kind: "test"})
	ev, ok := FromContext(ctx)
	if !ok {
		t.Fatalf("expected event in context")
	}

	gate := make(chan struct{})
	arrived := make(chan struct{}, workers)
	clock.setGate(gate, arrived)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_ = l.Checkpoint(ctx, "cp")
		}()
	}

	// Wait until every worker is blocked inside the clock, i.e. past the point
	// where the buggy code has read count but has not yet appended.
	for i := 0; i < workers; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(gate)
			t.Fatalf("timed out waiting for checkpoint workers to reach clock")
		}
	}
	close(gate)
	wg.Wait()

	ev.MuLock()
	got := len(ev.Checkpoints)
	ev.MuUnlock()

	if got != maxCheckpoints {
		t.Fatalf("concurrent Checkpoint produced %d checkpoints, want exactly %d (MaxCheckpoints)", got, maxCheckpoints)
	}
}
