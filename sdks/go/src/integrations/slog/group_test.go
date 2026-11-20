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

func TestWithGroupEmptyIsNoOp(t *testing.T) {
	// slog.Handler.WithGroup specifies that an empty name returns the receiver
	// unchanged (no grouping). It must not create a group with an empty key.
	base := Handler()
	if got := base.WithGroup(""); got != base {
		t.Fatalf("WithGroup(\"\") did not return the receiver unchanged")
	}

	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	logger := stdslog.New(Handler().WithGroup(""))
	logger.InfoContext(context.Background(), "request", stdslog.String("probe", "value"))

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	attrs := store.Events()[0].Attrs

	if _, ok := findGroup(attrs, ""); ok {
		t.Fatalf("unexpected empty-key group in output: %#v", attrs)
	}
	if got, ok := findString(attrs, "probe"); !ok || got != "value" {
		t.Fatalf("expected top-level probe attr, got %#v", attrs)
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

// Regression test for MASS-0476: an empty-key group must be flattened into the
// enclosing attr list instead of becoming Attr{Key:"", Value:[]loza.Attr}.
func TestToAttrInlinesEmptyKeyGroup(t *testing.T) {
	h := Handler()
	got := h.toAttr(stdslog.Group("",
		stdslog.String("user", "alice"),
		stdslog.Int("count", 3),
	))
	if len(got) != 2 {
		t.Fatalf("empty-key group converted to %d attrs, want 2: %#v", len(got), got)
	}
	if v, ok := findString(got, "user"); !ok || v != "alice" {
		t.Fatalf("inlined user attr = %q (ok=%v); %#v", v, ok, got)
	}
	for _, a := range got {
		if a.Key == "" {
			t.Fatalf("empty-key group wrapper was not inlined: %#v", a)
		}
	}
}

// The same inlining must apply when an empty-key group is nested inside a
// named group: its children become children of the named group.
func TestToAttrInlinesNestedEmptyKeyGroup(t *testing.T) {
	h := Handler()
	got := h.toAttr(stdslog.Group("outer",
		stdslog.String("keep", "yes"),
		stdslog.Group("", stdslog.String("inner", "value")),
	))
	if len(got) != 1 || got[0].Key != "outer" {
		t.Fatalf("named group conversion = %#v", got)
	}
	children, ok := got[0].Value.([]loza.Attr)
	if !ok {
		t.Fatalf("named group value = %#v", got[0].Value)
	}
	if v, ok := findString(children, "inner"); !ok || v != "value" {
		t.Fatalf("nested empty-key group was not inlined: %#v", children)
	}
	for _, a := range children {
		if a.Key == "" {
			t.Fatalf("empty-key wrapper inside named group: %#v", a)
		}
	}
}

func TestSlogHandlerInlinesEmptyKeyGroupAttrs(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

	logger := stdslog.New(Handler())
	// "method" and "path" are canonical loza keys, so use namespaced keys to
	// assert on custom attrs without the duplicate-field policy dropping them.
	logger.InfoContext(context.Background(), "request",
		stdslog.Group("", stdslog.String("http_method", "GET"), stdslog.String("http_path", "/")))

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	attrs := store.Events()[0].Attrs

	if v, ok := findString(attrs, "http_method"); !ok || v != "GET" {
		t.Fatalf("empty-key group children missing from event attrs: %#v", attrs)
	}
	if v, ok := findString(attrs, "http_path"); !ok || v != "/" {
		t.Fatalf("empty-key group children missing from event attrs: %#v", attrs)
	}
	for _, a := range attrs {
		if a.Key == "" {
			t.Fatalf("emitted event contains empty-key attr: %#v", a)
		}
	}
}

func TestWithGroupEmptyPreservesExistingGroups(t *testing.T) {
	base := Handler().WithGroup("http")
	if got := base.WithGroup(""); got != base {
		t.Fatalf("WithGroup(\"\") should return the receiver with existing groups intact")
	}
}

func TestEmptyKeyGroupAttrsAreInlined(t *testing.T) {
	// slog inlines empty-key groups: their children are promoted to the parent
	// instead of being wrapped in a group with an empty key.
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

	logger := stdslog.New(Handler())
	logger.InfoContext(context.Background(), "event",
		stdslog.Group("", stdslog.String("flat", "1"), stdslog.Group("nested", stdslog.String("inner", "2"))))

	if store.Len() != 1 {
		t.Fatalf("expected one emitted event, got %d", store.Len())
	}
	attrs := store.Events()[0].Attrs

	if _, ok := findGroup(attrs, ""); ok {
		t.Fatalf("unexpected empty-key group in output: %#v", attrs)
	}
	if got, ok := findString(attrs, "flat"); !ok || got != "1" {
		t.Fatalf("expected inlined flat attr, got %#v", attrs)
	}
	group, ok := findGroup(attrs, "nested")
	if !ok {
		t.Fatalf("expected nested group to be preserved: %#v", attrs)
	}
	if got, ok := findString(group, "inner"); !ok || got != "2" {
		t.Fatalf("expected nested.inner attr, got %#v", group)
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
