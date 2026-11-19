package slog

import (
	"context"
	stdslog "log/slog"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

func TestWithGroupBuildsNestedAttrs(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	logger := stdslog.New(Handler().WithGroup("http"))
	logger.InfoContext(context.Background(), "request", stdslog.String("method", "GET"))

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	ev := store.Events()[0]
	group, ok := findGroup(ev.Attrs, "http")
	if !ok {
		t.Fatalf("expected grouped attrs under http")
	}
	if got, ok := findString(group, "method"); !ok || got != "GET" {
		t.Fatalf("expected grouped http.method attr")
	}
}

// Regression test for MASS-0473: attributes added before a WithGroup must stay
// at the top level, while attributes added afterwards belong to that group.
func TestWithAttrsWithGroupCallOrder(t *testing.T) {
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	logger := stdslog.New(
		Handler().
			WithAttrs([]stdslog.Attr{stdslog.String("before", "top")}).
			WithGroup("g").
			WithAttrs([]stdslog.Attr{stdslog.String("after", "nested")}),
	)
	logger.InfoContext(context.Background(), "request", stdslog.String("record", "value"))

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	attrs := store.Events()[0].Attrs

	if got, ok := findString(attrs, "before"); !ok || got != "top" {
		t.Fatalf("expected top-level before=top, attrs = %#v", attrs)
	}
	group, ok := findGroup(attrs, "g")
	if !ok {
		t.Fatalf("expected grouped attrs under g, attrs = %#v", attrs)
	}
	if got, ok := findString(group, "after"); !ok || got != "nested" {
		t.Fatalf("expected g.after=nested, group = %#v", group)
	}
	if got, ok := findString(group, "record"); !ok || got != "value" {
		t.Fatalf("expected g.record=value, group = %#v", group)
	}
	if _, ok := findString(group, "before"); ok {
		t.Fatalf("pre-group attr must not be nested under g, group = %#v", group)
	}
}

// Nested WithGroup calls with attrs at multiple depths must merge into a
// single group tree rather than emitting a separate top-level group per depth.
func TestNestedWithGroupMergesSegments(t *testing.T) {
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	logger := stdslog.New(
		Handler().
			WithGroup("a").
			WithAttrs([]stdslog.Attr{stdslog.String("x", "1")}).
			WithGroup("b").
			WithAttrs([]stdslog.Attr{stdslog.String("y", "2")}),
	)
	logger.InfoContext(context.Background(), "nested")

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	attrs := store.Events()[0].Attrs

	if got := countKey(attrs, "a"); got != 1 {
		t.Fatalf("expected exactly one top-level group a, got %d (attrs = %#v)", got, attrs)
	}
	outer, ok := findGroup(attrs, "a")
	if !ok {
		t.Fatalf("expected grouped attrs under a, attrs = %#v", attrs)
	}
	if got, ok := findString(outer, "x"); !ok || got != "1" {
		t.Fatalf("expected a.x=1, outer = %#v", outer)
	}
	inner, ok := findGroup(outer, "b")
	if !ok {
		t.Fatalf("expected nested group a.b, outer = %#v", outer)
	}
	if got, ok := findString(inner, "y"); !ok || got != "2" {
		t.Fatalf("expected a.b.y=2, inner = %#v", inner)
	}
}

func countKey(attrs []loza.Attr, key string) int {
	n := 0
	for _, a := range attrs {
		if a.Key == key {
			n++
		}
	}
	return n
}

func findGroup(attrs []loza.Attr, key string) ([]loza.Attr, bool) {
	for _, a := range attrs {
		if a.Key != key {
			continue
		}
		children, ok := a.Value.([]loza.Attr)
		if ok {
			return children, true
		}
	}
	return nil, false
}

func findString(attrs []loza.Attr, key string) (string, bool) {
	for _, a := range attrs {
		if a.Key != key {
			continue
		}
		v, ok := a.Value.(string)
		return v, ok
	}
	return "", false
}
