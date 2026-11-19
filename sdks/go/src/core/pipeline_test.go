package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nopSinkWriter struct{}

func (nopSinkWriter) WriteEvent(context.Context, []byte, *Event) error { return nil }
func (nopSinkWriter) Flush(context.Context) error                      { return nil }
func (nopSinkWriter) Close(context.Context) error                      { return nil }

type failingSinkWriter struct{ err error }

func (s failingSinkWriter) WriteEvent(context.Context, []byte, *Event) error { return s.err }
func (failingSinkWriter) Flush(context.Context) error                        { return nil }
func (failingSinkWriter) Close(context.Context) error                        { return nil }

type gateSinkWriter struct {
	entered chan string
	gate    chan struct{}
	mu      sync.Mutex
	writes  []string
}

func newGateSinkWriter() *gateSinkWriter {
	return &gateSinkWriter{
		entered: make(chan string, 8),
		gate:    make(chan struct{}, 8),
	}
}

func (s *gateSinkWriter) WriteEvent(_ context.Context, encoded []byte, _ *Event) error {
	select {
	case s.entered <- string(encoded):
	default:
	}
	<-s.gate
	s.mu.Lock()
	s.writes = append(s.writes, string(encoded))
	s.mu.Unlock()
	return nil
}

func (s *gateSinkWriter) Flush(context.Context) error { return nil }
func (s *gateSinkWriter) Close(context.Context) error { return nil }

func (s *gateSinkWriter) snapshotWrites() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.writes))
	copy(out, s.writes)
	return out
}

func TestPipelineEnqueueAfterShutdown(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		QueueSize:     8,
		Workers:       1,
		FlushInterval: time.Second,
		Sinks:         []SinkWriter{nopSinkWriter{}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	_, err := p.Enqueue(PipelineItem{Encoded: []byte(`{}`)})
	if !errors.Is(err, ErrPipelineClosed) {
		t.Fatalf("expected ErrPipelineClosed, got %v", err)
	}
}

func TestPipelineWorkerHandlesClosedQueue(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		QueueSize:     1,
		Workers:       1,
		FlushInterval: time.Second,
		Sinks:         []SinkWriter{nopSinkWriter{}},
	})

	close(p.queue)
	p.stopOnce.Do(func() { close(p.done) })

	waitCh := make(chan struct{})
	go func() {
		p.workerWG.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker did not exit after queue close")
	}
}

func TestP0PipelineDropOldestPreservesNewest(t *testing.T) {
	sink := newGateSinkWriter()
	p := NewPipeline(PipelineConfig{
		QueueSize:     1,
		Workers:       1,
		FlushInterval: time.Hour,
		Backpressure:  DropOldest,
		Sinks:         []SinkWriter{sink},
	})

	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("one")}); err != nil || !ok {
		t.Fatalf("enqueue one failed: ok=%v err=%v", ok, err)
	}

	select {
	case got := <-sink.entered:
		if got != "one" {
			t.Fatalf("expected first write to be one, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("worker did not start first write")
	}

	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("two")}); err != nil || !ok {
		t.Fatalf("enqueue two failed: ok=%v err=%v", ok, err)
	}
	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("three")}); err != nil || !ok {
		t.Fatalf("enqueue three failed: ok=%v err=%v", ok, err)
	}

	for i := 0; i < 3; i++ {
		sink.gate <- struct{}{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	writes := sink.snapshotWrites()
	if len(writes) != 2 {
		t.Fatalf("expected 2 writes after drop-oldest, got %v", writes)
	}
	if writes[0] != "one" || writes[1] != "three" {
		t.Fatalf("expected writes [one three], got %v", writes)
	}
}

func TestPipelineWorkerReportsWriteErrors(t *testing.T) {
	writeErr := errors.New("sink write failed")
	errCh := make(chan error, 4)
	p := NewPipeline(PipelineConfig{
		QueueSize:     2,
		Workers:       1,
		FlushInterval: time.Hour,
		Sinks:         []SinkWriter{failingSinkWriter{err: writeErr}},
		OnError: func(err error) {
			errCh <- err
		},
	})

	ok, err := p.Enqueue(PipelineItem{Encoded: []byte("event")})
	if err != nil || !ok {
		t.Fatalf("enqueue failed: ok=%v err=%v", ok, err)
	}

	select {
	case got := <-errCh:
		if !errors.Is(got, writeErr) {
			t.Fatalf("expected write error, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected worker write error callback")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

// TestPipelineWorkerReportsWriteErrorOnce guards against the worker notifying
// OnError a second time for an error that writeBatch already reported.
func TestPipelineWorkerReportsWriteErrorOnce(t *testing.T) {
	writeErr := errors.New("sink write failed")
	var mu sync.Mutex
	var got []error
	p := NewPipeline(PipelineConfig{
		QueueSize:     2,
		Workers:       1,
		FlushInterval: time.Hour,
		Sinks:         []SinkWriter{failingSinkWriter{err: writeErr}},
		OnError: func(err error) {
			mu.Lock()
			got = append(got, err)
			mu.Unlock()
		},
	})

	ok, err := p.Enqueue(PipelineItem{Encoded: []byte("event")})
	if err != nil || !ok {
		t.Fatalf("enqueue failed: ok=%v err=%v", ok, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly one OnError call for the write error, got %d: %v", len(got), got)
	}
	if !errors.Is(got[0], writeErr) {
		t.Fatalf("expected write error, got %v", got[0])
	}
}

// timedFlushErrorSink returns err for the first left Flush calls and nil after.
type timedFlushErrorSink struct {
	err  error
	mu   sync.Mutex
	left int
}

func (s *timedFlushErrorSink) WriteEvent(context.Context, []byte, *Event) error { return nil }
func (s *timedFlushErrorSink) Close(context.Context) error                      { return nil }
func (s *timedFlushErrorSink) Flush(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.left > 0 {
		s.left--
		return s.err
	}
	return nil
}

// TestPipelineFlusherReportsFlushErrorOnce guards against the periodic flusher
// notifying OnError a second time for an error that flushSinks already reported.
func TestPipelineFlusherReportsFlushErrorOnce(t *testing.T) {
	flushErr := errors.New("sink flush failed")
	sink := &timedFlushErrorSink{err: flushErr, left: 1}
	errCh := make(chan error, 8)
	p := NewPipeline(PipelineConfig{
		QueueSize:     2,
		Workers:       1,
		FlushInterval: 10 * time.Millisecond,
		Sinks:         []SinkWriter{sink},
		OnError: func(err error) {
			errCh <- err
		},
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	}()

	select {
	case got := <-errCh:
		if !errors.Is(got, flushErr) {
			t.Fatalf("expected flush error, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected periodic flusher error callback")
	}

	// The duplicate used to fire back-to-back within the same flush cycle, so a
	// short settling window is enough to catch it without making the test flaky.
	select {
	case dup := <-errCh:
		t.Fatalf("OnError fired more than once for a single flush error: %v", dup)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPipelineConcurrentEnqueueAndFlushDoesNotLoseDrainWakeup(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		QueueSize:     1,
		Workers:       4,
		FlushInterval: time.Hour,
		Backpressure:  Block,
		Sinks:         []SinkWriter{nopSinkWriter{}},
	})

	for round := range 100 {
		var producers sync.WaitGroup
		for range 32 {
			producers.Add(1)
			go func() {
				defer producers.Done()
				ok, err := p.Enqueue(PipelineItem{Encoded: []byte(`{"event":"race"}`)})
				if err != nil || !ok {
					t.Errorf("enqueue failed: ok=%v err=%v", ok, err)
				}
			}()
		}
		producers.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := p.Flush(ctx); err != nil {
			cancel()
			t.Fatalf("flush round %d failed: %v (pending=%d)", round, err, p.pending.Load())
		}
		cancel()
		if pending := p.pending.Load(); pending != 0 {
			t.Fatalf("flush round %d left pending=%d", round, pending)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

// countingSinkWriter records every event delivered to the sink.
type countingSinkWriter struct{ writes atomic.Int64 }

func (s *countingSinkWriter) WriteEvent(context.Context, []byte, *Event) error {
	s.writes.Add(1)
	return nil
}
func (*countingSinkWriter) Flush(context.Context) error { return nil }
func (*countingSinkWriter) Close(context.Context) error { return nil }

func TestPipelineShutdownWaitsForInFlightEnqueueBeforeClosingDone(t *testing.T) {
	// Hold a worker inside WriteEvent and keep the queue full so that a third
	// Enqueue is left in flight (reserved but blocked on the queue send) when
	// Shutdown begins. Shutdown must not close done until that send completes.
	sink := newGateSinkWriter()
	p := NewPipeline(PipelineConfig{
		QueueSize:     1,
		Workers:       1,
		FlushInterval: time.Hour,
		Backpressure:  Block,
		Sinks:         []SinkWriter{sink},
	})

	blocker := make(chan struct{})
	go func() {
		defer close(blocker)
		ok, err := p.Enqueue(PipelineItem{Encoded: []byte("blocker")})
		if !ok || err != nil {
			t.Errorf("blocker enqueue failed: ok=%v err=%v", ok, err)
		}
	}()
	select {
	case got := <-sink.entered:
		if got != "blocker" {
			t.Fatalf("expected worker to start blocker, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start blocker write")
	}
	<-blocker

	if ok, err := p.Enqueue(PipelineItem{Encoded: []byte("filler")}); !ok || err != nil {
		t.Fatalf("filler enqueue failed: ok=%v err=%v", ok, err)
	}

	inflightDone := make(chan struct{})
	go func() {
		defer close(inflightDone)
		ok, err := p.Enqueue(PipelineItem{Encoded: []byte("inflight")})
		if !ok || err != nil {
			t.Errorf("inflight enqueue failed: ok=%v err=%v", ok, err)
		}
	}()

	// Wait until the third item is reserved and blocked on the full queue.
	deadline := time.Now().Add(2 * time.Second)
	for p.pending.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := p.pending.Load(); got < 3 {
		t.Fatalf("inflight enqueue did not reach pending=3 (got %d)", got)
	}

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- p.Shutdown(ctx)
	}()

	// The in-flight Enqueue is still blocked on the send, so done must remain
	// open. On the buggy code Shutdown closes done immediately and this fails.
	select {
	case <-p.done:
		t.Fatal("Shutdown closed done while an Enqueue was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	for range 3 {
		sink.gate <- struct{}{}
	}

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete after releasing in-flight enqueue")
	}
	select {
	case <-inflightDone:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight enqueue did not complete")
	}

	if writes := sink.snapshotWrites(); len(writes) != 3 {
		t.Fatalf("expected all 3 accepted events to be written, got %v", writes)
	}
}

func TestPipelineEnqueueRacingShutdownNeverLosesAcceptedEvents(t *testing.T) {
	for round := range 200 {
		sink := &countingSinkWriter{}
		p := NewPipeline(PipelineConfig{
			QueueSize:     1,
			Workers:       1,
			FlushInterval: time.Hour,
			Backpressure:  Block,
			Sinks:         []SinkWriter{sink},
		})

		const producers = 8
		var accepted atomic.Int64
		var producersWG sync.WaitGroup
		start := make(chan struct{})
		for range producers {
			producersWG.Add(1)
			go func() {
				defer producersWG.Done()
				<-start
				ok, err := p.Enqueue(PipelineItem{Encoded: []byte(`{"race":true}`)})
				if err != nil && !errors.Is(err, ErrPipelineClosed) {
					t.Errorf("unexpected enqueue error: %v", err)
				}
				if ok && err == nil {
					accepted.Add(1)
				}
			}()
		}
		close(start)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		shutdownErr := p.Shutdown(ctx)
		cancel()
		producersWG.Wait()
		if shutdownErr != nil {
			t.Fatalf("round %d: shutdown failed: %v (pending=%d)", round, shutdownErr, p.pending.Load())
		}
		if want, got := accepted.Load(), sink.writes.Load(); got != want {
			t.Fatalf("round %d: accepted %d events but sink wrote %d", round, want, got)
		}
	}
}
