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

// assertHookMapsLevels emits one event at each zerolog level through the
// adapter hook and asserts that all five reach loza. zerolog suppresses events
// below the process-wide global level even when the logger itself allows them,
// so the global level is explicitly raised to Trace (and restored afterwards)
// to make this assertion independent of other tests' global state.
func assertHookMapsLevels(t *testing.T) {
	t.Helper()

	prevLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() { zerolog.SetGlobalLevel(prevLevel) })

	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	zl := zerolog.New(&bytes.Buffer{}).Hook(Hook())

	// Debug and Trace both map to loza Debug.
	zl.Debug().Msg("debug msg")
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

// TestHookRunMapsLevelsToSeverity verifies that each zerolog level is routed to
// the correct loza severity. In particular, NoLevel (from logger.Log()/Send())
// and unknown/custom numeric levels are level-agnostic and must not be
// mislabeled as errors (issue #702).
func TestHookRunMapsLevelsToSeverity(t *testing.T) {
	tests := []struct {
		name  string
		level zerolog.Level
		want  loza.Level
	}{
		{"info", zerolog.InfoLevel, loza.LevelInfo},
		{"warn", zerolog.WarnLevel, loza.LevelWarn},
		{"error", zerolog.ErrorLevel, loza.LevelError},
		{"fatal", zerolog.FatalLevel, loza.LevelError},
		{"panic", zerolog.PanicLevel, loza.LevelError},
		{"no level", zerolog.NoLevel, loza.LevelInfo},
		{"custom numeric", zerolog.Level(42), loza.LevelInfo},
	}

	h := Hook()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink, store := loza.MemorySink()
			if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
				t.Fatalf("configure: %v", err)
			}

			h.Run(nil, tt.level, tt.name)

			events := store.Events()
			if len(events) != 1 {
				t.Fatalf("expected 1 emitted event, got %d", len(events))
			}
			if got := events[0].Level; got != tt.want {
				t.Fatalf("level = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHookRunMapsLevels(t *testing.T) {
	assertHookMapsLevels(t)
}

// TestHookRunMapsLevelsWithRestrictiveGlobalLevel is a regression test for the
// no-op "ensure trace is available" call: a restrictive global level left by
// another test must not suppress the Debug/Trace events this mapping relies on.
func TestHookRunMapsLevelsWithRestrictiveGlobalLevel(t *testing.T) {
	origLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	t.Cleanup(func() { zerolog.SetGlobalLevel(origLevel) })

	assertHookMapsLevels(t)
}
