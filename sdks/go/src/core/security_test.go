package core

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestEmitEnforcesMaxAttrCountWithTruncatedMarker(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.Security.MaxAttrCount = 2

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "security.max_attr_count"})
	_ = l.Finish(ctx, "success",
		String("first", "one"),
		String("second", "two"),
		String("third", "three"),
	)
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	payload := decodePayload(t, sink.last)
	attrs, ok := payload["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected attrs object, got %#v", payload["attrs"])
	}
	if attrs["first"] != "one" || attrs["second"] != "two" {
		t.Fatalf("expected first two attrs preserved, got %#v", payload)
	}
	if _, ok := attrs["third"]; ok {
		t.Fatalf("expected attr beyond MaxAttrCount to be dropped")
	}
	truncated, ok := attrs["_truncated"].(bool)
	if !ok || !truncated {
		t.Fatalf("expected _truncated=true marker, got %#v", attrs["_truncated"])
	}
}

func TestEmitTruncatesStringFieldsAtMaxFieldBytes(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.Security.MaxFieldBytes = 4

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "security.max_field_bytes"})
	_ = l.Finish(ctx, "success",
		String("top", "abcdefgh"),
		Group("meta", String("note", "wxyz1234"), Int("count", 7)),
	)
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	payload := decodePayload(t, sink.last)
	attrs, ok := payload["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected attrs object, got %#v", payload["attrs"])
	}
	if got := attrs["top"]; got != "abcd" {
		t.Fatalf("expected top field to be truncated to 4 bytes, got %#v", got)
	}

	meta, ok := attrs["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta group object, got %#v", attrs["meta"])
	}
	if got := meta["note"]; got != "wxyz" {
		t.Fatalf("expected nested string to be truncated to 4 bytes, got %#v", got)
	}
	if got := meta["count"]; got != float64(7) {
		t.Fatalf("expected non-string nested field unchanged, got %#v", got)
	}
}

func TestEmitDropsOversizedEventWhenConfigured(t *testing.T) {
	sink := &captureSink{}
	stats := &statsProbe{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.StatsHandler = stats
	cfg.Security.MaxEventBytes = 1
	cfg.Security.DropOversizedEvents = true

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "security.oversized_event"})
	_ = l.Finish(ctx, "success", String("payload", "this guarantees the encoded event is oversized"))
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	if len(sink.last) != 0 {
		t.Fatalf("expected oversized event to be dropped and not written to sink")
	}
	if len(stats.drops) != 1 || stats.drops[0] != "oversized_event" {
		t.Fatalf("expected oversized_event drop reason, got %#v", stats.drops)
	}
	if stats.emitCount != 0 {
		t.Fatalf("expected no emit callback for dropped oversized event")
	}
}

func TestApplySecurityRedactsSensitiveGroup(t *testing.T) {
	cfg := SecurityConfig{
		RedactByDefault: true,
		MaxFieldBytes:   4096,
	}
	attrs := []Attr{
		MarkSensitive(Group("creds", String("user", "alice"), String("password", "hunter2"))),
		Group("meta", String("note", "public")),
	}

	out := applySecurity(attrs, cfg)
	if len(out) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(out))
	}

	// A group keyed sensitive.* must be redacted as a whole: replacing its
	// value with [REDACTED] must also rewrite its kind so the children are
	// never emitted.
	creds := out[0]
	if creds.Key != "sensitive.creds" {
		t.Fatalf("expected sensitive.creds key, got %q", creds.Key)
	}
	if creds.Kind != KindString {
		t.Fatalf("expected sensitive group to be rewritten as a string, got kind %d", creds.Kind)
	}
	if creds.Value != "[REDACTED]" {
		t.Fatalf("expected sensitive group to be redacted, got %#v", creds.Value)
	}

	// Non-sensitive groups must keep their children untouched.
	meta := out[1]
	if meta.Kind != KindGroup {
		t.Fatalf("expected meta to remain a group, got kind %d", meta.Kind)
	}
	children, ok := meta.Value.([]Attr)
	if !ok || len(children) != 1 || children[0].Value != "public" {
		t.Fatalf("expected non-sensitive group children preserved, got %#v", meta.Value)
	}
}

func TestApplySecuritySensitiveGroupRespectsAllowPII(t *testing.T) {
	cfg := SecurityConfig{
		RedactByDefault: true,
		AllowPII:        true,
		MaxFieldBytes:   4096,
	}
	attrs := []Attr{
		MarkSensitive(Group("creds", String("password", "hunter2"))),
	}

	out := applySecurity(attrs, cfg)
	if len(out) != 1 || out[0].Kind != KindGroup {
		t.Fatalf("expected sensitive group preserved when AllowPII is true, got %#v", out)
	}
	children, ok := out[0].Value.([]Attr)
	if !ok || len(children) != 1 || children[0].Value != "hunter2" {
		t.Fatalf("expected group children preserved when AllowPII is true, got %#v", out[0].Value)
	}
}

func TestEmitRedactsSensitiveGroup(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.Security.RedactByDefault = true
	cfg.Security.AllowPII = false

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "security.sensitive_group"})
	_ = l.Finish(ctx, "success",
		MarkSensitive(Group("creds", String("user", "alice"), String("password", "hunter2"))),
		String("visible", "public"),
	)
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	payload := decodePayload(t, sink.last)
	attrs, ok := payload["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected attrs object, got %#v", payload["attrs"])
	}
	sensitive, ok := attrs["sensitive"].(map[string]any)
	if !ok {
		t.Fatalf("expected redacted sensitive object, got %#v", attrs["sensitive"])
	}
	if got := sensitive["creds"]; got != "[REDACTED]" {
		t.Fatalf("expected sensitive group redacted, got %#v", got)
	}
	if got := attrs["visible"]; got != "public" {
		t.Fatalf("expected unrelated attr preserved, got %#v", got)
	}
}

func decodePayload(t *testing.T, encoded []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(encoded), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return payload
}
