package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	loza "github.com/astraive/loza/sdks/go"
)

// recordingSink captures encoded events so tests can assert what was logged.
type recordingSink struct {
	events []string
}

func (s *recordingSink) Name() string { return "recording" }

func (s *recordingSink) WriteEvent(_ context.Context, encoded []byte, _ *loza.Event) error {
	s.events = append(s.events, string(encoded))
	return nil
}

func (s *recordingSink) Flush(context.Context) error { return nil }
func (s *recordingSink) Close(context.Context) error { return nil }

// TestRunAuditExampleReturnsAliasErrorWithoutDereference reproduces the
// reported behavior: Alias -> Child -> New can return (nil, err). The example
// must surface that error instead of calling Info on the nil logger and
// panicking. A regression here fails with a clear message.
func TestRunAuditExampleReturnsAliasErrorWithoutDereference(t *testing.T) {
	boom := errors.New("alias failed")
	makeAlias := func(string) (*loza.Logger, error) { return nil, boom }

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("runAuditExample panicked on a nil alias logger: %v", r)
		}
	}()

	err := runAuditExample(makeAlias)
	if !errors.Is(err, boom) {
		t.Fatalf("runAuditExample error = %v, want wrapped %v", err, boom)
	}
}

// TestRunAuditExampleLogsWhenAliasSucceeds confirms the happy path still emits
// the audit trail through the returned logger.
func TestRunAuditExampleLogsWhenAliasSucceeds(t *testing.T) {
	sink := &recordingSink{}
	logger, err := loza.New(loza.ApplyConfig(loza.Dev(), loza.WithSink(sink)))
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	makeAlias := func(string) (*loza.Logger, error) { return logger, nil }

	if err := runAuditExample(makeAlias); err != nil {
		t.Fatalf("runAuditExample returned unexpected error: %v", err)
	}

	got := strings.Join(sink.events, "")
	if !strings.Contains(got, "audit trail started") {
		t.Errorf("audit trail was not logged; got:\n%s", got)
	}
}

// TestInvalidConfigYieldsNilLogger documents the trigger for MASS-0372:
// a config validation failure makes loza.New return a nil logger alongside
// the error, so any subsequent method call on it would dereference nil.
func TestInvalidConfigYieldsNilLogger(t *testing.T) {
	logger, err := loza.New(loza.ApplyConfig(loza.Config{},
		loza.WithService("checkout-api"),
		loza.WithBatchSize(-1), // negative batch size always fails validation
	))
	if err == nil {
		t.Fatal("expected a config validation error, got nil")
	}
	if logger != nil {
		t.Fatalf("expected a nil logger on validation failure, got %v", logger)
	}
}

// TestNewCustomLoggerReturnsErrorOnInvalidConfig is the regression test for
// MASS-0372. Before the fix the example discarded the error and called
// logger.Info on the nil logger, panicking. The helper must instead return the
// error without panicking.
func TestNewCustomLoggerReturnsErrorOnInvalidConfig(t *testing.T) {
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("newCustomLogger panicked on invalid config (nil dereference): %v", r)
			}
		}()
		err = newCustomLogger(
			loza.WithService("checkout-api"),
			loza.WithBatchSize(-1),
		)
	}()

	if err == nil {
		t.Fatal("expected an error from an invalid config, got nil")
	}
	if !strings.Contains(err.Error(), "create custom logger") {
		t.Fatalf("expected a contextual error message, got %q", err.Error())
	}
}

// TestNewCustomLoggerSucceedsWithValidConfig guards against over-correcting:
// a valid config must still produce a usable logger and log the readiness msg.
func TestNewCustomLoggerSucceedsWithValidConfig(t *testing.T) {
	err := newCustomLogger(
		loza.WithService("checkout-api"),
		loza.WithCollectorEndpoint("http://localhost:9308"),
		loza.WithSink(loza.NewMockSink()),
	)
	if err != nil {
		t.Fatalf("expected valid config to succeed, got %v", err)
	}
}
