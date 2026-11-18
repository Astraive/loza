package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

// captureErrorOutput swaps errorOutput for the duration of fn and returns what
// was written, so tests can observe errors that would otherwise be discarded.
func captureErrorOutput(t *testing.T, fn func()) string {
	t.Helper()
	previous := errorOutput
	var buf bytes.Buffer
	errorOutput = &buf
	defer func() { errorOutput = previous }()
	fn()
	return buf.String()
}

// configureTestSDK installs a hermetic SDK (no network, synchronous delivery)
// so tests can exercise real mutating calls.
func configureTestSDK(t *testing.T) {
	t.Helper()
	cfg := loza.Test()
	cfg.Sinks = []loza.Sink{loza.NoopSink()}
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure test SDK: %v", err)
	}
}

func TestReportErrorNil(t *testing.T) {
	out := captureErrorOutput(t, func() {
		if err := reportError("Enrich", nil); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output for a nil error, got %q", out)
	}
}

func TestReportErrorSurfacesAndLogsError(t *testing.T) {
	sentinel := errors.New("boom")
	var got error
	out := captureErrorOutput(t, func() {
		got = reportError("Enrich", sentinel)
	})
	if got == nil {
		t.Fatal("expected the error to be returned, not discarded")
	}
	if !errors.Is(got, sentinel) {
		t.Fatalf("expected the returned error to wrap the sentinel, got %v", got)
	}
	if !strings.Contains(out, "Enrich") || !strings.Contains(out, "boom") {
		t.Fatalf("expected the error to be logged, got %q", out)
	}
}

// TestMutatingCallErrorIsSurfaced reproduces the reported behavior: a mutating
// SDK call on a closed event returns an error that must no longer be silently
// discarded (Enrich's return value was previously ignored by the demo).
func TestMutatingCallErrorIsSurfaced(t *testing.T) {
	configureTestSDK(t)

	ctx := loza.StartEvent(context.Background(), loza.Params{Event: "test.closed"})
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	enrichErr := loza.Enrich(ctx, loza.String("key", "value"))
	if enrichErr == nil {
		t.Fatal("expected Enrich on a closed event to fail")
	}
	var closed *loza.EventClosedError
	if !errors.As(enrichErr, &closed) {
		t.Fatalf("expected EventClosedError, got %T %v", enrichErr, enrichErr)
	}

	var got error
	out := captureErrorOutput(t, func() {
		got = reportError("Enrich", enrichErr)
	})
	if got == nil || !errors.As(got, &closed) {
		t.Fatalf("expected the reported closed-event error to be returned, got %v", got)
	}
	if !strings.Contains(out, "Enrich") || !strings.Contains(out, "closed") {
		t.Fatalf("expected the closed-event error to be logged, got %q", out)
	}
}

// TestHappyPathPropagatesMutatingCallError drives the actual demo lifecycle
// code with a closed event and verifies the first mutating call's error is
// reported and propagated rather than ignored.
func TestHappyPathPropagatesMutatingCallError(t *testing.T) {
	configureTestSDK(t)

	ctx := loza.StartEvent(context.Background(), loza.Params{Event: "order.process"})
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	var got error
	out := captureErrorOutput(t, func() {
		got = happyPathWithContext(ctx)
	})
	if got == nil {
		t.Fatal("expected the demo to propagate the failed mutating call")
	}
	var closed *loza.EventClosedError
	if !errors.As(got, &closed) {
		t.Fatalf("expected EventClosedError, got %T %v", got, got)
	}
	if !strings.Contains(out, "Enrich") {
		t.Fatalf("expected the failed Enrich to be logged, got %q", out)
	}
}

// TestDemoFunctionsSucceedOnHappyPath guards the healthy path so enforcing
// error checks does not break the demo's normal execution.
func TestDemoFunctionsSucceedOnHappyPath(t *testing.T) {
	configureTestSDK(t)

	fns := map[string]func() error{
		"happyPath":                     happyPath,
		"errorHandling":                 errorHandling,
		"invalidTransitions":            invalidTransitions,
		"arrayOperations":               arrayOperations,
		"demonstrateStateObservability": demonstrateStateObservability,
	}
	for name, fn := range fns {
		if err := fn(); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}
