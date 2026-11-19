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
	"context"
	"strings"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

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
