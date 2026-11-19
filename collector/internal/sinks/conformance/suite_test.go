package conformance

import (
	"reflect"
	"testing"
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
	"context"
	"errors"
	"fmt"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

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
