package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type captureSink struct {
	last []byte
}

func (s *captureSink) Name() string { return "capture" }

func (s *captureSink) WriteEvent(_ context.Context, encoded []byte, _ *Event) error {
	s.last = append(s.last[:0], encoded...)
	return nil
}

func (s *captureSink) Flush(_ context.Context) error { return nil }

func (s *captureSink) Close(_ context.Context) error { return nil }

func TestRunEventPanicStackRespectsIncludeSource(t *testing.T) {
	cases := []struct {
		name          string
		includeSource bool
		wantStack     bool
	}{
		{name: "exclude by default", includeSource: false, wantStack: false},
		{name: "include when enabled", includeSource: true, wantStack: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &captureSink{}
			cfg := Test()
			cfg.Sinks = []Sink{sink}
			cfg.IncludeSource = tc.includeSource
			cfg.PanicRecovery = true

			l, err := New(cfg)
			if err != nil {
				t.Fatalf("new logger: %v", err)
			}

			prev := Default()
			SetDefault(l)
			defer SetDefault(prev)

			err = RunEvent(context.Background(), Params{Event: "panic.test"}, func(context.Context) error {
				panic("boom")
			})
			if err == nil {
				t.Fatalf("expected panic recovery error")
			}

			if len(sink.last) == 0 {
				t.Fatalf("expected emitted event")
			}

			var payload map[string]any
			if uerr := json.Unmarshal(bytes.TrimSpace(sink.last), &payload); uerr != nil {
				t.Fatalf("unmarshal event: %v", uerr)
			}

			errObj, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error object in payload")
			}
			_, hasStack := errObj["stack"]
			if hasStack != tc.wantStack {
				t.Fatalf("stack presence mismatch: got=%v want=%v", hasStack, tc.wantStack)
			}
		})
	}
}

// classifiedPanicError is an error that also carries classification metadata.
// When passed to panic, a recovery wrapper that does not unwrap will hide all
// of this from errors.Is/errors.As and from DefaultErrorExtractor.
type classifiedPanicError struct {
	code      string
	retriable bool
}

func (e *classifiedPanicError) Error() string   { return "classified panic: " + e.code }
func (e *classifiedPanicError) Code() string    { return e.code }
func (e *classifiedPanicError) Retriable() bool { return e.retriable }

func TestRunEventPanicPreservesErrorIdentity(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.PanicRecovery = true

// setupRunEventLogger installs a logger with a capture sink as the default and
// returns a cleanup that restores the previous default.
func setupRunEventLogger(t *testing.T) {
	t.Helper()
	cfg := Test()
	cfg.Sinks = []Sink{&captureSink{}}
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	prev := Default()
	SetDefault(l)
	defer SetDefault(prev)

	sentinel := &classifiedPanicError{code: "E_PANIC", retriable: true}
	err = RunEvent(context.Background(), Params{Event: "panic.identity"}, func(context.Context) error {
		panic(sentinel)
	})
	if err == nil {
		t.Fatalf("expected panic recovery error")
	}

	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is did not reach the original panicked error")
	}

	var coded CodedError
	if !errors.As(err, &coded) || coded.Code() != sentinel.code {
		t.Fatalf("error code did not propagate: got=%v want=%q", coded, sentinel.code)
	}
	var retriable RetriableError
	if !errors.As(err, &retriable) || !retriable.Retriable() {
		t.Fatalf("retriability did not propagate")
	}

	info := DefaultErrorExtractor(err)
	if info.Code != sentinel.code {
		t.Fatalf("extracted code: got=%q want=%q", info.Code, sentinel.code)
	}
	if !info.Retriable {
		t.Fatalf("extracted retriable: got=false want=true")
	}
	if info.Cause == "" {
		t.Fatalf("expected extracted cause to preserve the original error message")
	}
}

func TestPanicRunErrorUnwrapNonError(t *testing.T) {
	e := &panicRunError{value: "boom"}
	if got := errors.Unwrap(e); got != nil {
		t.Fatalf("Unwrap of a non-error panic value: got=%v want=nil", got)
	}
	if got := DefaultErrorExtractor(e).Code; got != "" {
		t.Fatalf("code from non-error panic value: got=%q want empty", got)
	}

	var nilErr *panicRunError
	if got := errors.Unwrap(nilErr); got != nil {
		t.Fatalf("Unwrap of nil receiver: got=%v want=nil", got)
	}
}
	prev := Default()
	SetDefault(l)
	t.Cleanup(func() { SetDefault(prev) })
}

// TestRunEventPropagatesFinishError verifies that a failure to record the
// outcome in the deferred Finish is returned to the caller instead of being
// silently discarded. The event is emitted inside fn so it is already closed
// when the wrapper's deferred Finish runs, which makes Finish return an
// EventClosedError.
func TestRunEventPropagatesFinishError(t *testing.T) {
	setupRunEventLogger(t)

	err := RunEvent(context.Background(), Params{Event: "finish.error"}, func(ctx context.Context) error {
		return Default().Emit(ctx)
	})
	if err == nil {
		t.Fatalf("expected Finish failure to be propagated, got nil")
	}

	var closed *EventClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("expected EventClosedError, got %T: %v", err, err)
	}
}

// TestRunEventPreservesApplicationErrorWhenFinishFails verifies that combining
// a Finish failure into the named return does not lose the original error from
// fn; both remain observable to the caller.
func TestRunEventPreservesApplicationErrorWhenFinishFails(t *testing.T) {
	setupRunEventLogger(t)

	appErr := errors.New("boom")
	err := RunEvent(context.Background(), Params{Event: "finish.error.with.fn"}, func(ctx context.Context) error {
		_ = Default().Emit(ctx)
		return appErr
	})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if !errors.Is(err, appErr) {
		t.Fatalf("expected application error to be preserved, got %v", err)
	}
	if !strings.Contains(err.Error(), "finish failed") {
		t.Fatalf("expected Finish failure to be combined into the error, got %v", err)
	}
}

