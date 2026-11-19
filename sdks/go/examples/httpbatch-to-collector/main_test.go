package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDeliverAndShutdownSurfacesErrors is the regression test for the defect
// where delivery and shutdown errors were discarded via defer. Both failures
// must now be reported to the caller.
func TestDeliverAndShutdownSurfacesErrors(t *testing.T) {
	emitErr := errors.New("sink delivery failed")
	shutdownErr := errors.New("shutdown failed")

	var calls []string
	emit := func(context.Context) error {
		calls = append(calls, "emit")
		return emitErr
	}
	shutdown := func(context.Context) error {
		calls = append(calls, "shutdown")
		return shutdownErr
	}

	err := deliverAndShutdown(context.Background(), emit, shutdown)
	if err == nil {
		t.Fatal("deliverAndShutdown() returned nil, want both errors surfaced")
	}
	if !errors.Is(err, emitErr) {
		t.Errorf("error does not wrap emit error: %v", err)
	}
	if !errors.Is(err, shutdownErr) {
		t.Errorf("error does not wrap shutdown error: %v", err)
	}
	for _, want := range []string{"emit event", "shutdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not identify %q", err, want)
		}
	}

	// Emit must still run before Shutdown, matching the original defer order.
	if got, want := strings.Join(calls, ","), "emit,shutdown"; got != want {
		t.Errorf("call order = %q, want %q", got, want)
	}
}

func TestDeliverAndShutdownSingleError(t *testing.T) {
	emitErr := errors.New("emit only")

	err := deliverAndShutdown(context.Background(),
		func(context.Context) error { return emitErr },
		func(context.Context) error { return nil },
	)
	if !errors.Is(err, emitErr) {
		t.Errorf("error = %v, want wrapped emit error", err)
	}
}

func TestDeliverAndShutdownNoErrors(t *testing.T) {
	called := false
	err := deliverAndShutdown(context.Background(),
		func(context.Context) error { called = true; return nil },
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Errorf("deliverAndShutdown() = %v, want nil", err)
	}
	if !called {
		t.Error("emit was not called")
	}
}
