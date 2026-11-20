package core

import (
	"errors"
	"strings"
	"testing"
)

// lookupDotNode walks a DotNode tree by path segments, returning nil when a
// segment is missing.
func lookupDotNode(root *DotNode, path ...string) *DotNode {
	node := root
	for _, part := range path {
		if node == nil {
			return nil
		}
		node = node.Children[part]
	}
	return node
}

func assertLeaf(t *testing.T, node *DotNode, kind uint8, value any) {
	t.Helper()
	if node == nil {
		t.Fatalf("node is missing")
	}
	if node.Value == nil {
		t.Fatalf("node has no leaf value")
	}
	if node.Value.Kind != kind {
		t.Errorf("kind = %d, want %d", node.Value.Kind, kind)
	}
	if node.Value.Value != value {
		t.Errorf("value = %v, want %v", node.Value.Value, value)
	}
}

func TestExpandDotKeysLeafBeforeBranch(t *testing.T) {
	// Regression for MASS-0416: "a.b" followed by "a.b.c" used to write to a
	// nil Children map (panic), or replaced the leaf and dropped the branch.
	_, _, _, keys, roots, err := ExpandDotKeys(
		[]string{"a.b", "a.b.c"},
		[]uint8{1, 2},
		[]any{"v-b", "v-c"},
	)
	if err != nil {
		t.Fatalf("ExpandDotKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("keys = %v, want [a]", keys)
	}
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}

	b := lookupDotNode(roots[0], "b")
	assertLeaf(t, b, 1, "v-b")
	if b.Children == nil {
		t.Fatalf("node a.b lost its children")
	}
	assertLeaf(t, lookupDotNode(roots[0], "b", "c"), 2, "v-c")
}

func TestExpandDotKeysBranchBeforeLeaf(t *testing.T) {
	// The reverse order used to overwrite the branch node at "b", silently
	// dropping the child "c".
	_, _, _, keys, roots, err := ExpandDotKeys(
		[]string{"a.b.c", "a.b"},
		[]uint8{2, 1},
		[]any{"v-c", "v-b"},
	)
	if err != nil {
		t.Fatalf("ExpandDotKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("keys = %v, want [a]", keys)
	}
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}

	b := lookupDotNode(roots[0], "b")
	assertLeaf(t, b, 1, "v-b")
	assertLeaf(t, lookupDotNode(roots[0], "b", "c"), 2, "v-c")
}

func TestExpandDotKeysDuplicateLeafIsLastWriteWins(t *testing.T) {
	_, _, _, keys, roots, err := ExpandDotKeys(
		[]string{"a.b", "a.b"},
		[]uint8{1, 2},
		[]any{"first", "second"},
	)
	if err != nil {
		t.Fatalf("ExpandDotKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want [a]", keys)
	}
	assertLeaf(t, lookupDotNode(roots[0], "b"), 2, "second")
}

func TestExpandDotKeysNonConflicting(t *testing.T) {
	plainKeys, _, _, keys, roots, err := ExpandDotKeys(
		[]string{"standalone", "user.id", "user.name", "plain"},
		[]uint8{1, 2, 3, 4},
		[]any{"s", 42, "alice", "p"},
	)
	if err != nil {
		t.Fatalf("ExpandDotKeys: %v", err)
	}
	if len(plainKeys) != 2 || plainKeys[0] != "standalone" || plainKeys[1] != "plain" {
		t.Fatalf("plainKeys = %v, want [standalone plain]", plainKeys)
	}
	if len(keys) != 1 || keys[0] != "user" {
		t.Fatalf("keys = %v, want [user]", keys)
	}
	assertLeaf(t, lookupDotNode(roots[0], "id"), 2, 42)
	assertLeaf(t, lookupDotNode(roots[0], "name"), 3, "alice")
}

func TestExpandDotKeysRejectsMismatchedSliceLengths(t *testing.T) {
	cases := []struct {
		name   string
		keys   []string
		kinds  []uint8
		values []any
	}{
		{
			name:   "shorter kinds",
			keys:   []string{"user.id", "plain"},
			kinds:  []uint8{1},
			values: []any{"u1", "v"},
		},
		{
			name:   "shorter values",
			keys:   []string{"user.id", "plain"},
			kinds:  []uint8{1, 2},
			values: []any{"u1"},
		},
		{
			name:   "longer kinds",
			keys:   []string{"plain"},
			kinds:  []uint8{1, 2},
			values: []any{"v"},
		},
		{
			name:   "longer values",
			keys:   []string{"user.id"},
			kinds:  []uint8{1},
			values: []any{"u1", "extra"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plainKeys, plainKinds, plainValues, groupKeys, groupRoots, err :=
				ExpandDotKeys(tc.keys, tc.kinds, tc.values)

			if !errors.Is(err, ErrDotKeyLengthMismatch) {
				t.Fatalf("expected ErrDotKeyLengthMismatch, got %v", err)
			}
			if err == nil || !strings.Contains(err.Error(), "keys=") {
				t.Fatalf("error should report the observed lengths, got %v", err)
			}
			if plainKeys != nil || plainKinds != nil || plainValues != nil ||
				groupKeys != nil || groupRoots != nil {
				t.Fatalf("expected no partial results on mismatch, got %v %v %v %v %v",
					plainKeys, plainKinds, plainValues, groupKeys, groupRoots)
			}
		})
	}
}

func TestExpandDotKeysWithEqualLengths(t *testing.T) {
	keys := []string{"plain", "user.id", "user.profile.name"}
	kinds := []uint8{1, 2, 3}
	values := []any{"p", "u1", "Ada"}

	plainKeys, plainKinds, plainValues, groupKeys, groupRoots, err :=
		ExpandDotKeys(keys, kinds, values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plainKeys) != 1 || plainKeys[0] != "plain" ||
		len(plainKinds) != 1 || plainKinds[0] != 1 ||
		len(plainValues) != 1 || plainValues[0] != "p" {
		t.Fatalf("unexpected plain attrs: %v %v %v", plainKeys, plainKinds, plainValues)
	}

	if len(groupKeys) != 1 || groupKeys[0] != "user" || len(groupRoots) != 1 {
		t.Fatalf("unexpected groups: %v %v", groupKeys, groupRoots)
	}
	root := groupRoots[0]
	id := root.Children["id"]
	if id == nil || id.Value == nil || id.Value.Kind != 2 || id.Value.Value != "u1" {
		t.Fatalf("unexpected user.id leaf: %#v", id)
	}
	profile := root.Children["profile"]
	if profile == nil || profile.Children["name"] == nil ||
		profile.Children["name"].Value.Value != "Ada" {
		t.Fatalf("unexpected user.profile.name leaf: %#v", profile)
	}
}

func TestExpandDotKeysEmptyInputs(t *testing.T) {
	plainKeys, plainKinds, plainValues, groupKeys, groupRoots, err :=
		ExpandDotKeys(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plainKeys)+len(plainKinds)+len(plainValues)+len(groupKeys)+len(groupRoots) != 0 {
		t.Fatal("expected empty results for empty inputs")
	}
}
