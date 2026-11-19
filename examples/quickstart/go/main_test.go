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
