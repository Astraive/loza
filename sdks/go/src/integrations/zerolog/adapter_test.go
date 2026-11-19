package zerolog

import (
	"bytes"
	"context"
	"testing"

	"github.com/astraive/loza/sdks/go"
	"github.com/rs/zerolog"
)

func TestHookCompatibility(t *testing.T) {
	// Hook() must return a non-nil value that satisfies zerolog.Hook.
	var hook zerolog.Hook = Hook()
	if hook == nil {
		t.Fatal("Hook() returned a nil zerolog.Hook")
	}

	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

	zl := zerolog.New(&bytes.Buffer{}).Hook(hook)
	zl.Info().Msg("compatibility check")

	if store.Len() != 1 {
		t.Fatalf("expected 1 emitted event, got %d", store.Len())
	}
}

func TestHookRunEmitsToLova(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	zl := zerolog.New(&bytes.Buffer{}).Hook(Hook())
	zl.Info().Msg("hello from zerolog")

	if store.Len() != 1 {
		t.Fatalf("expected 1 emitted event, got %d", store.Len())
	}
}

func TestHookRunMapsLevels(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	zl := zerolog.New(&bytes.Buffer{}).Hook(Hook())

	// Debug and Trace both map to loza Debug.
	zl.Debug().Msg("debug msg")
	zerolog.GlobalLevel() // ensure trace is available
	zl.Trace().Msg("trace msg")
	zl.Info().Msg("info msg")
	zl.Warn().Msg("warn msg")
	zl.Error().Msg("error msg")

	if store.Len() != 5 {
		t.Fatalf("expected 5 emitted events, got %d", store.Len())
	}
}

type zerologCtxKey struct{}

type contextCaptureSink struct {
	values []any
}

func (s *contextCaptureSink) Name() string { return "zerolog-context-capture" }

func (s *contextCaptureSink) WriteEvent(ctx context.Context, _ []byte, _ *loza.Event) error {
	s.values = append(s.values, ctx.Value(zerologCtxKey{}))
	return nil
}

func (s *contextCaptureSink) Flush(context.Context) error { return nil }
func (s *contextCaptureSink) Close(context.Context) error { return nil }

// TestHookRunForwardsEventContext verifies that the context attached to a
// zerolog Event via Event.Ctx is forwarded to loza's context-aware sinks
// instead of being discarded in favor of context.Background.
func TestHookRunForwardsEventContext(t *testing.T) {
	sink := &contextCaptureSink{}
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	const want = "trace-from-zerolog"
	ctx := context.WithValue(context.Background(), zerologCtxKey{}, want)

	zl := zerolog.New(&bytes.Buffer{}).Hook(Hook())
	zl.Info().Ctx(ctx).Msg("contextual message")

	if len(sink.values) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(sink.values))
	}
	if sink.values[0] != want {
		t.Fatalf("expected sink to receive event context value %q, got %#v", want, sink.values[0])
	}
}
