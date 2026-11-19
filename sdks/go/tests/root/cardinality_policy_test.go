package loza_test

import (
	"testing"

	"github.com/astraive/loza/sdks/go"
)

// TestCardinalityPolicyIsNotIdentity reproduces MASS-0383: the original
// implementation returned its input map verbatim, so it neither validated the
// policy nor protected callers from shared-state aliasing.
func TestCardinalityPolicyIsNotIdentity(t *testing.T) {
	input := map[string]any{"mode": "drop"}

	got := loza.CardinalityPolicy(input)

	// The result must be a defensive copy, not the very same map.
	got["injected"] = true
	if _, leaked := input["injected"]; leaked {
		t.Fatal("CardinalityPolicy returned its input map; mutations leaked back to the caller")
	}
	input["mode"] = "hash"
	if got["mode"] != "drop" {
		t.Fatalf("CardinalityPolicy result aliases input: mode = %v", got["mode"])
	}
}

func TestCardinalityPolicyEnforcesLimit(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		mode  string
		limit int
	}{
		{"missing limit gets default", map[string]any{}, "drop", 1000},
		{"zero limit gets default", map[string]any{"limit": 0}, "drop", 1000},
		{"negative limit gets default", map[string]any{"limit": -5}, "drop", 1000},
		{"negative int64 limit gets default", map[string]any{"limit": int64(-5)}, "drop", 1000},
		{"valid limit preserved", map[string]any{"limit": 25}, "drop", 25},
		{"valid int64 limit preserved", map[string]any{"limit": int64(25)}, "drop", 25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := loza.CardinalityPolicy(tc.input)
			if got["mode"] != tc.mode {
				t.Fatalf("mode = %v, want %q", got["mode"], tc.mode)
			}
			limit, ok := got["limit"].(int)
			if !ok {
				t.Fatalf("limit has type %T, want int", got["limit"])
			}
			if limit != tc.limit {
				t.Fatalf("limit = %d, want %d", limit, tc.limit)
			}
		})
	}
}

func TestCardinalityPolicyPreservesValidPolicy(t *testing.T) {
	input := map[string]any{
		"mode":   "sample",
		"limit":  50,
		"keys":   []any{"user.id", "tenant.id"},
		"future": "preserved",
	}
	got := loza.CardinalityPolicy(input)
	if got["mode"] != "sample" || got["limit"] != 50 {
		t.Fatalf("valid policy not preserved: %#v", got)
	}
	if _, ok := got["keys"]; !ok {
		t.Fatal("keys not preserved")
	}
	if got["future"] != "preserved" {
		t.Fatal("unknown keys not preserved for forward compatibility")
	}
}

func TestCardinalityPolicyInvalidModeDefaultsToDrop(t *testing.T) {
	for _, mode := range []any{"", "bogus", 42, nil} {
		got := loza.CardinalityPolicy(map[string]any{"mode": mode})
		if got["mode"] != "drop" {
			t.Fatalf("mode %v: got %q, want %q", mode, got["mode"], "drop")
		}
	}
}
