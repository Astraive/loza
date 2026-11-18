package loza_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

func TestDuplicatePolicyCanonicalWinsDropsConflicts(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.DuplicateFieldPolicy = loza.CanonicalWins
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event:   "duplicate.canonical_wins",
		Service: "svc-original",
	})
	_ = loza.Enrich(ctx,
		loza.String("service", "svc-attr"),
		loza.Int("status_code", 299),
		loza.String("tenant.id", "t-1"),
	)
	_ = loza.Finish(ctx, "success")
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	if got := store.Len(); got != 1 {
		t.Fatalf("expected 1 event, got %d", got)
	}
	ev := store.Events()[0]
	if ev.Service != "svc-original" {
		t.Fatalf("expected canonical service, got %q", ev.Service)
	}
	for _, a := range ev.AttrList() {
		if a.Key == "service" || a.Key == "status_code" {
			t.Fatalf("expected duplicate attrs to be dropped, got key=%q", a.Key)
		}
	}
}

func TestDuplicatePolicyAttrWinsOverwritesCanonical(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.DuplicateFieldPolicy = loza.UserWins
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event:   "duplicate.attr_wins",
		Service: "svc-original",
	})
	_ = loza.Enrich(ctx,
		loza.String("service", "svc-attr"),
		loza.Int("status_code", 201),
	)
	_ = loza.Finish(ctx, "success")
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	ev := store.Events()[0]
	if ev.Service != "svc-attr" {
		t.Fatalf("expected attr value to win, got %q", ev.Service)
	}
	if ev.StatusCode != 201 {
		t.Fatalf("expected status_code=201, got %d", ev.StatusCode)
	}
	for _, a := range ev.AttrList() {
		if a.Key == "service" || a.Key == "status_code" {
			t.Fatalf("expected duplicate attrs removed after canonical overwrite, got key=%q", a.Key)
		}
	}
}

func TestDuplicatePolicyKeepBothMovesUnderAttrs(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.DuplicateFieldPolicy = loza.KeepBothUnderAttrs
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event:   "duplicate.keep_both",
		Service: "svc-original",
	})
	_ = loza.Enrich(ctx, loza.String("service", "svc-attr"))
	_ = loza.Finish(ctx, "success")
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	ev := store.Events()[0]
	if ev.Service != "svc-original" {
		t.Fatalf("expected canonical service unchanged, got %q", ev.Service)
	}
	found := false
	for _, a := range ev.AttrList() {
		if a.Key == "attrs.service" {
			t.Fatalf("duplicate attr must not retain an 'attrs.' prefix on the internal key, got key=%q", a.Key)
		}
		if a.Key == "service" {
			found = true
			if v, ok := a.Value.(string); !ok || v != "svc-attr" {
				t.Fatalf("unexpected service attr value: %#v", a.Value)
			}
		}
	}
	if !found {
		t.Fatalf("expected duplicate attr to be kept under the canonical service key")
	}

	// Wire output must contain a single top-level "attrs" object, not the
	// doubly nested "attrs":{"attrs":{"service":...}} produced previously.
	var payload map[string]any
	if err := json.Unmarshal(store.Raw()[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	attrsObj, ok := payload["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level attrs object, got %#v", payload["attrs"])
	}
	if got := attrsObj["service"]; got != "svc-attr" {
		t.Fatalf("expected attrs.service == svc-attr, got %#v", got)
	}
	if nested, ok := attrsObj["attrs"]; ok {
		t.Fatalf("attrs must not be doubly nested; got attrs.attrs = %#v", nested)
	}
}

func TestDuplicatePolicyErrorOnDuplicate(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.DuplicateFieldPolicy = loza.ErrorOnDuplicate
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{
		Event:   "duplicate.error",
		Service: "svc-original",
	})
	_ = loza.Enrich(ctx, loza.String("service", "svc-attr"))
	_ = loza.Finish(ctx, "success")
	err := loza.Emit(ctx)
	if err == nil {
		t.Fatalf("expected duplicate policy error")
	}
	var dupErr *loza.DuplicateFieldError
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected DuplicateFieldError, got %T", err)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("expected no emitted events on duplicate error, got %d", got)
	}
}
