package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type pipelineCtxKey struct{}

// ctxCaptureSink records the context used for batch writes.
type ctxCaptureSink struct {
	seen chan context.Context
}

func (s *ctxCaptureSink) WriteEvent(context.Context, []byte, *Event) error { return nil }
func (s *ctxCaptureSink) Flush(context.Context) error                      { return nil }
func (s *ctxCaptureSink) Close(context.Context) error                      { return nil }
func (s *ctxCaptureSink) WriteBatch(ctx context.Context, _ []PipelineItem) error {
	s.seen <- ctx
	return nil
}

func TestPipelinePropagatesCallerContextToWrite(t *testing.T) {
	sink := &ctxCaptureSink{seen: make(chan context.Context, 4)}
	p := NewPipeline(PipelineConfig{
		QueueSize:     8,
		Workers:       1,
		FlushInterval: time.Hour,
		Sinks:         []SinkWriter{sink},
	})

	caller := context.WithValue(context.Background(), pipelineCtxKey{}, "caller-42")
	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("event"), Ctx: caller}); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}

	select {
	case got := <-sink.seen:
		if value := got.Value(pipelineCtxKey{}); value != "caller-42" {
			t.Fatalf("caller context value = %v, want caller-42", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sink did not receive a batch write")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// cancelBlockingSink blocks until the write context is canceled.
type cancelBlockingSink struct {
	entered chan struct{}
	once    sync.Once
}

func (s *cancelBlockingSink) WriteEvent(ctx context.Context, _ []byte, _ *Event) error {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return ctx.Err()
}
func (s *cancelBlockingSink) Flush(context.Context) error { return nil }
func (s *cancelBlockingSink) Close(context.Context) error { return nil }

func TestPipelineHonorsCallerCancellation(t *testing.T) {
	sink := &cancelBlockingSink{entered: make(chan struct{})}
	p := NewPipeline(PipelineConfig{
		QueueSize:     8,
		Workers:       1,
		FlushInterval: time.Hour,
		Sinks:         []SinkWriter{sink},
	})

	caller, cancel := context.WithCancel(context.Background())
	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("event"), Ctx: caller}); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	select {
	case <-sink.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start the write")
	}
	cancel()

	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after caller cancellation: %v", err)
	}
}

// flushConcurrencySink detects overlapping Flush calls.
type flushConcurrencySink struct {
	active    atomic.Int64
	maxActive atomic.Int64
	flushes   atomic.Int64
}

func (s *flushConcurrencySink) WriteEvent(context.Context, []byte, *Event) error { return nil }
func (s *flushConcurrencySink) Flush(context.Context) error {
	n := s.active.Add(1)
	for {
		m := s.maxActive.Load()
		if n <= m || s.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(time.Millisecond) // widen the window for a race to be observed
	s.flushes.Add(1)
	s.active.Add(-1)
	return nil
}
func (s *flushConcurrencySink) Close(context.Context) error { return nil }

func TestPipelineSerializesFlushAndStopsFlusher(t *testing.T) {
	sink := &flushConcurrencySink{}
	p := NewPipeline(PipelineConfig{
		QueueSize:     16,
		Workers:       2,
		FlushInterval: time.Millisecond,
		Sinks:         []SinkWriter{sink},
	})

	var callers sync.WaitGroup
	for range 16 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := p.Flush(ctx); err != nil {
				t.Errorf("concurrent flush: %v", err)
			}
		}()
	}
	callers.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := sink.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent Flush calls = %d, want 1", got)
	}

	// The flusher goroutine is tracked by Shutdown and must no longer flush.
	before := sink.flushes.Load()
	time.Sleep(20 * time.Millisecond)
	if after := sink.flushes.Load(); after != before {
		t.Fatalf("flusher still running after shutdown: flushes %d -> %d", before, after)
	}
}

// closeOnErrorSink blocks a write and counts Close calls.
type closeOnErrorSink struct {
	entered     chan struct{}
	block       chan struct{}
	closed      atomic.Int32
	enteredOnce sync.Once
}

func (s *closeOnErrorSink) WriteEvent(context.Context, []byte, *Event) error {
	s.enteredOnce.Do(func() { close(s.entered) })
	<-s.block
	return nil
}
func (s *closeOnErrorSink) Flush(context.Context) error { return nil }
func (s *closeOnErrorSink) Close(context.Context) error {
	s.closed.Add(1)
	return nil
}

func TestPipelineShutdownClosesSinksWhenDrainFails(t *testing.T) {
	sink := &closeOnErrorSink{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	p := NewPipeline(PipelineConfig{
		QueueSize:     4,
		Workers:       1,
		FlushInterval: time.Hour,
		Sinks:         []SinkWriter{sink},
	})
	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("blocked")}); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	select {
	case <-sink.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start the write")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := p.Shutdown(ctx)
	if err == nil {
		t.Fatal("shutdown should report the drain failure")
	}
	if got := sink.closed.Load(); got != 1 {
		t.Fatalf("sink Close calls = %d, want 1 on shutdown error path", got)
	}

	close(sink.block)
	waitDone := make(chan struct{})
	go func() {
		p.workerWG.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after the gate was released")
	}
}
