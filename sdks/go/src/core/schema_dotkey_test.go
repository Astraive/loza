package core

import (
	"reflect"
	"testing"
)

// These tests pin dot-key expansion to the canonical attrsToMap/setPath
// implementation in schema.go. A parallel implementation used to live in
// dotkey.go (ExpandDotKeys/DotNode/LeafValue); it was dead code and has been
// removed so the two paths cannot drift. The encoder calls attrsToMap, so this
// is the single source of truth for dot-key expansion.
func TestAttrsToMapDotKeyExpansion(t *testing.T) {
	t.Run("expands a single dot key into a nested object", func(t *testing.T) {
		attrs := []Attr{String("user.id", "u-1")}
		got := attrsToMap(attrs, true)
		want := map[string]any{"user": map[string]any{"id": "u-1"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("expands multi-level dot keys", func(t *testing.T) {
		attrs := []Attr{Int("http.request.retry.count", 3)}
		got := attrsToMap(attrs, true)
		want := map[string]any{
			"http": map[string]any{
				"request": map[string]any{
					"retry": map[string]any{"count": 3},
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("merges dot keys sharing a top-level prefix", func(t *testing.T) {
		attrs := []Attr{
			String("user.id", "u-1"),
			String("user.name", "Ada"),
			Bool("user.active", true),
		}
		got := attrsToMap(attrs, true)
		want := map[string]any{
			"user": map[string]any{
				"id":     "u-1",
				"name":   "Ada",
				"active": true,
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("keeps plain keys alongside expanded dot keys", func(t *testing.T) {
		attrs := []Attr{
			String("service", "api"),
			String("user.id", "u-1"),
			Int("status", 200),
		}
		got := attrsToMap(attrs, true)
		want := map[string]any{
			"service": "api",
			"user":    map[string]any{"id": "u-1"},
			"status":  200,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("leaves dotted keys flat when expansion is disabled", func(t *testing.T) {
		attrs := []Attr{String("user.id", "u-1"), String("service", "api")}
		got := attrsToMap(attrs, false)
		want := map[string]any{"user.id": "u-1", "service": "api"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("merges a dot key with an explicit Group under the same key", func(t *testing.T) {
		attrs := []Attr{
			Group("user", String("name", "Ada")),
			String("user.id", "u-1"),
		}
		got := attrsToMap(attrs, true)
		want := map[string]any{
			"user": map[string]any{"name": "Ada", "id": "u-1"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("skips attrs with an empty key", func(t *testing.T) {
		attrs := []Attr{{Key: "", Kind: KindString, Value: "ignored"}, String("user.id", "u-1")}
		got := attrsToMap(attrs, true)
		want := map[string]any{"user": map[string]any{"id": "u-1"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attrsToMap() = %#v, want %#v", got, want)
		}
	})

	t.Run("returns nil for no attrs", func(t *testing.T) {
		if got := attrsToMap(nil, true); got != nil {
			t.Fatalf("attrsToMap(nil) = %#v, want nil", got)
		}
	})
}
