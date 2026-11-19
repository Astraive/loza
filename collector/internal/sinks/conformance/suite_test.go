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
