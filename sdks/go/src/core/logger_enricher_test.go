package core

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

type enricherCtxKey string

func TestEmitAppliesContextEnricher(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	cfg.Enricher = func(ctx context.Context) []Attr {
		v, _ := ctx.Value(enricherCtxKey("tenant")).(string)
		return []Attr{String("tenant.id", v)}
	}

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := context.WithValue(context.Background(), enricherCtxKey("tenant"), "tenant-1")
	ctx = l.StartEvent(ctx, Params{Event: "enricher.test"})
	_ = l.Finish(ctx, "success")
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(sink.last), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	tenantObj, ok := payload["tenant"].(map[string]any)
	if !ok {
		t.Fatalf("expected tenant object in payload")
	}
	if tenantObj["id"] != "tenant-1" {
		t.Fatalf("expected tenant.id from enricher")
	}
}

func TestEmitIdempotentWithEnricher(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	enricherCalls := 0
	cfg.Enricher = func(context.Context) []Attr {
		enricherCalls++
		return []Attr{String("tenant.id", "enriched")}
	}

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "enricher.idempotent"})
	if err := l.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := l.Emit(ctx); err != nil {
		t.Fatalf("first emit: %v", err)
	}
	// A deferred + explicit Emit (or any repeated call) must be a no-op and
	// must not try to mutate the already-emitted event with enricher attrs.
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("second emit should be idempotent, got %v", err)
	}
	if enricherCalls != 1 {
		t.Fatalf("enricher should be applied exactly once, got %d calls", enricherCalls)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(sink.last), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	tenantObj, ok := payload["tenant"].(map[string]any)
	if !ok || tenantObj["id"] != "enriched" {
		t.Fatalf("expected tenant.id=enriched in emitted payload, got %v", payload["tenant"])
	}
}

func TestSetReplacesDuplicateEnrichedAttrsOnWire(t *testing.T) {
	for _, prior := range [][]Attr{
		{String("phase", "first")},
		{String("phase", "first"), String("phase", "second")},
	} {
		sink := &captureSink{}
		cfg := Test()
		cfg.Sinks = []Sink{sink}
		l, err := New(cfg)
		if err != nil {
			t.Fatalf("new logger: %v", err)
		}

		ctx := l.StartEvent(context.Background(), Params{Event: "set.duplicate"})
		if err := l.Enrich(ctx, prior...); err != nil {
			t.Fatalf("enrich: %v", err)
		}
		if err := l.Set(ctx, String("phase", "replacement")); err != nil {
			t.Fatalf("set: %v", err)
		}
		if got, ok := l.Get(ctx, "phase"); !ok || got != "replacement" {
			t.Fatalf("Get(phase) = %v, %t; want replacement, true", got, ok)
		}
		if err := l.Finish(ctx, "success"); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if err := l.Emit(ctx); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if err := l.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(sink.last), &payload); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		attrs, ok := payload["attrs"].(map[string]any)
		if !ok || attrs["phase"] != "replacement" {
			t.Fatalf("wire attrs.phase = %v; want replacement", payload["attrs"])
		}
	}
}


func TestSetPreservesAttributeOrderOnWire(t *testing.T) {
	sink := &captureSink{}
	cfg := Test()
	cfg.Sinks = []Sink{sink}
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "set.order"})
	if err := l.Enrich(ctx, String("a.b", "dotted"), String("a", "scalar")); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if err := l.Set(ctx, String("a.b", "replacement")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := l.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := l.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(sink.last), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	attrs, ok := payload["attrs"].(map[string]any)
	if !ok || attrs["a"] != "scalar" {
		t.Fatalf("wire attrs.a = %v; want scalar", payload["attrs"])
	}
}

