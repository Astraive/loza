package core

import (
	"reflect"
	"testing"
)

// TestParamsWithDoesNotAliasSiblingCustom verifies that two Params derived from
// the same base with With do not share (and therefore overwrite) the same
// Custom backing array. See issue #665.
func TestParamsWithDoesNotAliasSiblingCustom(t *testing.T) {
	// Give base.Custom spare capacity so that append(p.Custom, attrs...) would
	// reuse the backing array instead of allocating a fresh one.
	base := Params{Custom: make([]Attr, 0, 2)}

	a := base.With(String("a", "1"))
	b := base.With(String("b", "2"))

	wantA := []Attr{String("a", "1")}
	wantB := []Attr{String("b", "2")}

	if !reflect.DeepEqual(a.Custom, wantA) {
		t.Fatalf("a.Custom = %v, want %v (sibling With corrupted it)", a.Custom, wantA)
	}
	if !reflect.DeepEqual(b.Custom, wantB) {
		t.Fatalf("b.Custom = %v, want %v", b.Custom, wantB)
	}
	if len(a.Custom) > 0 && len(b.Custom) > 0 && &a.Custom[0] == &b.Custom[0] {
		t.Fatal("a.Custom and b.Custom share the same backing array")
	}
}

// TestParamsWithPreservesBaseAndExistingAttrs verifies With is a true copy: the
// receiver is untouched and pre-existing custom attrs are retained.
func TestParamsWithPreservesBaseAndExistingAttrs(t *testing.T) {
	base := Params{Custom: []Attr{String("base", "v")}}

	derived := base.With(String("extra", "v"))

	wantBase := []Attr{String("base", "v")}
	wantDerived := []Attr{String("base", "v"), String("extra", "v")}

	if !reflect.DeepEqual(base.Custom, wantBase) {
		t.Fatalf("base.Custom = %v, want %v (With mutated its receiver)", base.Custom, wantBase)
	}
	if !reflect.DeepEqual(derived.Custom, wantDerived) {
		t.Fatalf("derived.Custom = %v, want %v", derived.Custom, wantDerived)
	}
}
