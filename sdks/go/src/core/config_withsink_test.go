package core

import (
	"context"
	"testing"
)

// namedConfigSink is a distinguishable Sink used to verify which sink instance
// ends up in each Config derived via WithSink.
type namedConfigSink struct{ name string }

func (s *namedConfigSink) Name() string                                     { return s.name }
func (s *namedConfigSink) WriteEvent(context.Context, []byte, *Event) error { return nil }
func (s *namedConfigSink) Flush(context.Context) error                      { return nil }
func (s *namedConfigSink) Close(context.Context) error                      { return nil }

// TestWithSinkDoesNotMutateSharedBackingArray reproduces the case where two
// configs derived from the same parent share the Sinks backing array. With a
// spare-capacity parent slice, append writes derived sinks into the same index,
// so deriving z after y overwrites y's last sink.
func TestWithSinkDoesNotMutateSharedBackingArray(t *testing.T) {
	const spareCapacity = 4
	parent := Config{Sinks: make([]Sink, 2, spareCapacity)}
	parent.Sinks[0] = &namedConfigSink{name: "a"}
	parent.Sinks[1] = &namedConfigSink{name: "b"}

	x := parent.WithSink(&namedConfigSink{name: "x"})
	y := x.WithSink(&namedConfigSink{name: "y"})
	z := x.WithSink(&namedConfigSink{name: "z"})

	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "x", cfg: x, want: "x"},
		{name: "y", cfg: y, want: "y"},
		{name: "z", cfg: z, want: "z"},
	} {
		got := tc.cfg.Sinks[len(tc.cfg.Sinks)-1].Name()
		if got != tc.want {
			t.Errorf("%s.Sinks last sink = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The parent config's visible sinks must remain unchanged as well.
	if len(parent.Sinks) != 2 {
		t.Fatalf("parent.Sinks length = %d, want 2", len(parent.Sinks))
	}
	if got := parent.Sinks[0].Name(); got != "a" {
		t.Errorf("parent.Sinks[0] = %q, want %q", got, "a")
	}
	if got := parent.Sinks[1].Name(); got != "b" {
		t.Errorf("parent.Sinks[1] = %q, want %q", got, "b")
	}
}
