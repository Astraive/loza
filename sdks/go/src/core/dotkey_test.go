package core

import "testing"

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
	_, _, _, keys, roots := ExpandDotKeys(
		[]string{"a.b", "a.b.c"},
		[]uint8{1, 2},
		[]any{"v-b", "v-c"},
	)
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
	_, _, _, keys, roots := ExpandDotKeys(
		[]string{"a.b.c", "a.b"},
		[]uint8{2, 1},
		[]any{"v-c", "v-b"},
	)
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
	_, _, _, keys, roots := ExpandDotKeys(
		[]string{"a.b", "a.b"},
		[]uint8{1, 2},
		[]any{"first", "second"},
	)
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want [a]", keys)
	}
	assertLeaf(t, lookupDotNode(roots[0], "b"), 2, "second")
}

func TestExpandDotKeysNonConflicting(t *testing.T) {
	plainKeys, _, _, keys, roots := ExpandDotKeys(
		[]string{"standalone", "user.id", "user.name", "plain"},
		[]uint8{1, 2, 3, 4},
		[]any{"s", 42, "alice", "p"},
	)
	if len(plainKeys) != 2 || plainKeys[0] != "standalone" || plainKeys[1] != "plain" {
		t.Fatalf("plainKeys = %v, want [standalone plain]", plainKeys)
	}
	if len(keys) != 1 || keys[0] != "user" {
		t.Fatalf("keys = %v, want [user]", keys)
	}
	assertLeaf(t, lookupDotNode(roots[0], "id"), 2, 42)
	assertLeaf(t, lookupDotNode(roots[0], "name"), 3, "alice")
}
