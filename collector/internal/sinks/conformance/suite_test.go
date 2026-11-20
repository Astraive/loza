package conformance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

// TestOptionsExposesOnlyEffectiveOptions guards against reintroducing a no-op
// option such as RequiresIdempotent. That option used to be declared here but
// was never read by Run, so a caller setting it got identical behavior either
// way. It was removed rather than backed by a check that cannot verify its
// contract through the write-only event.Sink interface.
func TestOptionsExposesOnlyEffectiveOptions(t *testing.T) {
	typ := reflect.TypeOf(Options{})

	if _, ok := typ.FieldByName("RequiresIdempotent"); ok {
		t.Fatal("Options still exposes RequiresIdempotent, which Run never honored")
	}

	// Every option that remains must actually influence Run.
	effective := map[string]bool{
		"Name":              true,
		"Factory":           true,
		"LargePayloadBytes": true,
		"Timeout":           true,
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !effective[name] {
			t.Errorf("Options.%s does not influence Run and should be removed", name)
		}
	}
}

// nameRecordingSink is a no-op sink used to observe the testing.TB handed to
// the Factory for each conformance subtest.
type nameRecordingSink struct{}

func (nameRecordingSink) Name() string { return "name-recording" }

func (nameRecordingSink) WriteEvent(context.Context, []byte, *collectorevent.Event) error {
	return nil
}

func (nameRecordingSink) Flush(context.Context) error { return nil }

func (nameRecordingSink) Close(context.Context) error { return nil }

func TestRunNamesSubtestsWithName(t *testing.T) {
	checks := []string{
		"open_close",
		"write_one_event",
		"write_batch_serial",
		"flush",
		"large_payload",
		"context_cancellation",
	}
	for _, tc := range []struct {
		label      string
		name       string
		wantPrefix string
	}{
		{label: "explicit", name: "custom-sink", wantPrefix: "custom-sink/"},
		{label: "default", name: "", wantPrefix: "sink/"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var names []string
			factory := func(tb testing.TB) collectorevent.Sink {
				names = append(names, tb.Name())
				return nameRecordingSink{}
			}
			Run(t, Options{Name: tc.name, Factory: factory, LargePayloadBytes: 16})

			if len(names) != len(checks) {
				t.Fatalf("got %d subtest names, want %d: %v", len(names), len(checks), names)
			}
			for i, check := range checks {
				want := tc.wantPrefix + check
				if !strings.HasSuffix(names[i], want) {
					t.Errorf("subtest %d name = %q, want suffix %q", i, names[i], want)
				}
			}
		})
	}
}

// TestReportsCancelled is the regression test for the context_cancellation
// conformance check. The old check (`err != nil && !errors.Is(err,
// context.Canceled)`) accepted err == nil, so a sink that ignores the
// pre-cancelled context and successfully writes the event passed. The check
// must now require a demonstrable context.Canceled.
func TestReportsCancelled(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error from context-ignoring sink", err: nil, want: false},
		{name: "context.Canceled", err: context.Canceled, want: true},
		{name: "wrapped context.Canceled", err: fmt.Errorf("write: %w", context.Canceled), want: true},
		{name: "unrelated error", err: errors.New("boom"), want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reportsCancelled(tt.err); got != tt.want {
				t.Fatalf("reportsCancelled(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// honoringSink returns ctx.Err(), so it demonstrably honors a pre-cancelled
// context while still succeeding for the suite's normal write/flush checks.
type honoringSink struct{}

func (honoringSink) Name() string { return "honoring" }

func (honoringSink) WriteEvent(ctx context.Context, _ []byte, _ *collectorevent.Event) error {
	return ctx.Err()
}

func (honoringSink) Flush(context.Context) error { return nil }
func (honoringSink) Close(context.Context) error { return nil }

// TestRunAcceptsCancellationHonoringSink exercises the whole suite end to end
// and confirms a sink that honors cancellation still passes every check,
// including the now-strict context_cancellation assertion.
func TestRunAcceptsCancellationHonoringSink(t *testing.T) {
	Run(t, Options{
		Name:    "honoring",
		Factory: func(testing.TB) collectorevent.Sink { return honoringSink{} },
	})
}
