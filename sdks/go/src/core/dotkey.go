package core

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDotKeyLengthMismatch is returned by ExpandDotKeys when the parallel
// keys, kinds and values slices do not have the same length.
var ErrDotKeyLengthMismatch = errors.New("core: dot-key input slices must have equal length")

// DotNode is an intermediate tree node used to merge dot-key attrs
// (e.g. "user.id", "user.name") into nested JSON objects.
type DotNode struct {
	// Value holds the attr value recorded for this node. A node that was only
	// ever a leaf has Children nil. When a shorter and a longer dot key collide
	// on the same path (e.g. "a.b" and "a.b.c") both the value and the children
	// are preserved by the deterministic merge in ExpandDotKeys.
	Value *LeafValue
	// Children maps sub-key → child node.
	Children map[string]*DotNode
}

// LeafValue carries the typed value of a leaf node.
type LeafValue struct {
	Kind  uint8 // matches loza.Kind
	Value any
}

// ExpandDotKeys takes a flat list of (key, kind, value) tuples and converts
// all dot-separated keys into a nested tree. Non-dot keys are left as-is.
//
// When dot keys collide on the same path (e.g. both "a.b" and "a.b.c" are
// present), the nodes are merged deterministically: a leaf value never
// replaces an existing branch, and a branch never drops an existing value.
// Repeated identical leaf keys use last-write-wins.
//
// Returns five slices:
//   - plainKeys/plainKinds/plainValues: attrs whose keys have no dot
//   - groupKeys/groupRoots: top-level keys mapped to merged nested objects
func ExpandDotKeys(keys []string, kinds []uint8, values []any) (
	plainKeys []string, plainKinds []uint8, plainValues []any,
	groupKeys []string, groupRoots []*DotNode, err error,
) {
	if len(keys) != len(kinds) || len(keys) != len(values) {
		return nil, nil, nil, nil, nil, fmt.Errorf(
			"%w: keys=%d kinds=%d values=%d",
			ErrDotKeyLengthMismatch, len(keys), len(kinds), len(values),
		)
	}

	// Temporary map: top-level key → root DotNode
	roots := make(map[string]*DotNode)
	var rootOrder []string // preserve insertion order

	for i, key := range keys {
		dot := strings.IndexByte(key, '.')
		if dot < 0 {
			// No dot: plain attr
			plainKeys = append(plainKeys, key)
			plainKinds = append(plainKinds, kinds[i])
			plainValues = append(plainValues, values[i])
			continue
		}

		// Split "user.id.extra" → ["user", "id", "extra"]
		parts := strings.Split(key, ".")
		top := parts[0]

		root, exists := roots[top]
		if !exists {
			root = &DotNode{Children: make(map[string]*DotNode)}
			roots[top] = root
			rootOrder = append(rootOrder, top)
		}

		// Drill down, creating intermediate nodes. If an intermediate segment
		// was previously recorded as a leaf value, keep that value and attach
		// children to the same node instead of replacing it.
		cur := root
		for _, part := range parts[1 : len(parts)-1] {
			child, ok := cur.Children[part]
			if !ok {
				child = &DotNode{Children: make(map[string]*DotNode)}
				cur.Children[part] = child
			} else if child.Children == nil {
				child.Children = make(map[string]*DotNode)
			}
			cur = child
		}

		// The final segment is a leaf value. Merge into any node that already
		// exists at this position rather than replacing it: a longer dot key
		// may have created a branch here, and replacing the whole node would
		// silently drop that branch (and could write to a nil Children map).
		leaf := parts[len(parts)-1]
		node, ok := cur.Children[leaf]
		if !ok {
			node = &DotNode{}
			cur.Children[leaf] = node
		}
		node.Value = &LeafValue{Kind: kinds[i], Value: values[i]}
	}

	for _, k := range rootOrder {
		groupKeys = append(groupKeys, k)
		groupRoots = append(groupRoots, roots[k])
	}
	return
}
