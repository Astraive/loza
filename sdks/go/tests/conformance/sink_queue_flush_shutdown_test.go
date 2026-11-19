package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

func TestSinkQueueFlushAndShutdownHelpers(t *testing.T) {
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink).WithAsync(false)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := loza.Default().Event(context.Background(), "sink.event", loza.String("family", "sink")); err != nil {
		t.Fatalf("event: %v", err)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if store.Len() == 0 {
		t.Fatalf("expected sink event to be flushed")
	}

	// memorySink implements none of Drainable/Pauseable/Sized/Checkable, so these
	// calls must take the documented no-op fallbacks. They exercise the fallback
	// paths; TestSinkHelpersDispatchToOptionalInterfaces exercises the real ones.
	if err := loza.Drain(context.Background(), sink); err != nil {
		t.Fatalf("drain: %v", err)
	}
	loza.Pause(sink)
	loza.Resume(sink)

	// Assert the fallback contract explicitly: memorySink does not implement
	// Sized or Checkable, so QueueSize must be 0 and Health must be nil. These
	// assertions fail if memorySink gains either interface (or the fallback
	// behaviour changes), which the previous unchecked-value assertions could not.
	if got := loza.QueueSize(sink); got != 0 {
		t.Fatalf("expected fallback queue size 0 for unsupported sink, got %d", got)
	}
	if err := loza.Health(context.Background(), sink); err != nil {
		t.Fatalf("expected nil health error for unsupported sink, got %v", err)
	}
	if loza.StdoutSink() == nil {
		t.Fatalf("expected stdout sink")
	}
	if loza.NoopSink() == nil {
		t.Fatalf("expected noop sink")
	}
	if loza.MultiSink(sink, loza.NoopSink()) == nil {
		t.Fatalf("expected multi sink")
	}
	if err := loza.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// optionalIfaceSink implements Sink plus every optional sink interface
// (Drainable, Pauseable, Sized, Checkable). It records which methods the
// helper functions actually dispatch to, so the assertions can detect a
// regression that silently falls back to a no-op.
type optionalIfaceSink struct {
	queueSize int
	healthErr error
	drainErr  error

	drained bool
	paused  bool
	resumed bool
	flushed bool
}

func (s *optionalIfaceSink) Name() string { return "optional-iface" }

func (s *optionalIfaceSink) WriteEvent(context.Context, []byte, *loza.Event) error {
	return nil
}

func (s *optionalIfaceSink) Flush(context.Context) error {
	s.flushed = true
	return nil
}

func (s *optionalIfaceSink) Close(context.Context) error { return nil }

func (s *optionalIfaceSink) Drain(context.Context) error {
	s.drained = true
	return s.drainErr
}

func (s *optionalIfaceSink) Pause()  { s.paused = true }
func (s *optionalIfaceSink) Resume() { s.resumed = true }

func (s *optionalIfaceSink) QueueSize() int { return s.queueSize }

func (s *optionalIfaceSink) Health(context.Context) error { return s.healthErr }

func TestSinkHelpersDispatchToOptionalInterfaces(t *testing.T) {
	ctx := context.Background()
	drainErr := errors.New("drain failed")
	healthErr := errors.New("sink unhealthy")
	s := &optionalIfaceSink{queueSize: 7, healthErr: healthErr, drainErr: drainErr}

	// Drain must call Drainable.Drain (not the Flush fallback) and propagate its error.
	if err := loza.Drain(ctx, s); !errors.Is(err, drainErr) {
		t.Fatalf("Drain() error = %v, want %v", err, drainErr)
	}
	if !s.drained {
		t.Fatal("Drain did not call the sink's Drain method")
	}
	if s.flushed {
		t.Fatal("Drain called the Flush fallback despite the sink implementing Drainable")
	}

	// Pause/Resume must reach the sink rather than silently doing nothing.
	loza.Pause(s)
	if !s.paused {
		t.Fatal("Pause did not call the sink's Pause method")
	}
	loza.Resume(s)
	if !s.resumed {
		t.Fatal("Resume did not call the sink's Resume method")
	}

	// QueueSize must return the sink's reported size, not the unsupported fallback 0.
	if got := loza.QueueSize(s); got != s.queueSize {
		t.Fatalf("QueueSize() = %d, want %d", got, s.queueSize)
	}

	// Health must return the sink's error, not the unsupported fallback nil.
	if err := loza.Health(ctx, s); !errors.Is(err, healthErr) {
		t.Fatalf("Health() error = %v, want %v", err, healthErr)
	}
}
