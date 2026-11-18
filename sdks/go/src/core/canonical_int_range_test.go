package core

import (
	"math"
	"testing"
)

// Regression for MASS-0420: uint64 values above the destination type's maximum
// must not silently wrap to a negative value when applied to canonical fields.
func TestCanonicalIntRejectsOutOfRangeUint64(t *testing.T) {
	if got, ok := canonicalInt(uint64(math.MaxInt64) + 1); ok {
		t.Fatalf("canonicalInt accepted out-of-range uint64: got %d", got)
	}
	if got, ok := canonicalInt(uint64(math.MaxUint64)); ok {
		t.Fatalf("canonicalInt accepted MaxUint64: got %d", got)
	}

	// In-range values must still be accepted unchanged.
	got, ok := canonicalInt(uint64(200))
	if !ok || got != 200 {
		t.Fatalf("canonicalInt(200) = (%d, %v), want (200, true)", got, ok)
	}
}

func TestCanonicalInt64RejectsOutOfRangeUint64(t *testing.T) {
	if got, ok := canonicalInt64(uint64(math.MaxInt64) + 1); ok {
		t.Fatalf("canonicalInt64 accepted out-of-range uint64: got %d", got)
	}
	if got, ok := canonicalInt64(uint64(math.MaxUint64)); ok {
		t.Fatalf("canonicalInt64 accepted MaxUint64: got %d", got)
	}

	// In-range values must still be accepted unchanged.
	got, ok := canonicalInt64(uint64(math.MaxInt64))
	if !ok || got != math.MaxInt64 {
		t.Fatalf("canonicalInt64(MaxInt64) = (%d, %v), want (%d, true)", got, ok, int64(math.MaxInt64))
	}
}

func TestSetRejectsOutOfRangeUint64CanonicalAttrs(t *testing.T) {
	t.Run("status_code", func(t *testing.T) {
		ev := &Event{}
		if err := ev.Set(Attr{Key: "status_code", Value: uint64(math.MaxUint64)}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if ev.StatusCode == -1 {
			t.Fatalf("status_code silently wrapped to -1")
		}
		if ev.StatusCode != 0 {
			t.Fatalf("status_code = %d, want 0 (rejected)", ev.StatusCode)
		}
		if _, ok := attrValue(ev.Attrs, "status_code"); !ok {
			t.Fatalf("rejected value should be retained as a generic attr")
		}
	})

	t.Run("duration_ms", func(t *testing.T) {
		ev := &Event{}
		if err := ev.Set(Attr{Key: "duration_ms", Value: uint64(math.MaxUint64)}); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if ev.DurationMS == -1 {
			t.Fatalf("duration_ms silently wrapped to -1")
		}
		if ev.DurationMS != 0 {
			t.Fatalf("duration_ms = %d, want 0 (rejected)", ev.DurationMS)
		}
		if _, ok := attrValue(ev.Attrs, "duration_ms"); !ok {
			t.Fatalf("rejected value should be retained as a generic attr")
		}
	})
}
