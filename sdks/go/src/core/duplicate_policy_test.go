package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// TestApplyDuplicateFieldPolicyNilAndEmpty guards the early-return behavior
// that must be preserved when the empty-attrs check moves under the mutex.
func TestApplyDuplicateFieldPolicyNilAndEmpty(t *testing.T) {
	if err := applyDuplicateFieldPolicy(nil, ErrorOnDuplicate); err != nil {
		t.Fatalf("nil event: %v", err)
	}
	if err := applyDuplicateFieldPolicy(&Event{}, ErrorOnDuplicate); err != nil {
		t.Fatalf("empty event: %v", err)
	}
}

// TestApplyDuplicateFieldPolicyConcurrentAttrs exercises applyDuplicateFieldPolicy
// against AddAttrs, which mutates ev.Attrs under the event mutex. Under -race this
// reproduces the data race caused by reading the ev.Attrs slice header before
// acquiring ev.mu; after the fix every access is made under the lock.
func TestApplyDuplicateFieldPolicyConcurrentAttrs(t *testing.T) {
	ev := &Event{}

	const (
		writers = 4
		readers = 4
		iters   = 250
	)

	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < iters; j++ {
				attrs := []Attr{{Key: fmt.Sprintf("custom.%d.%d", id, j), Value: j}}
				if err := ev.AddAttrs(attrs); err != nil {
					t.Errorf("AddAttrs: %v", err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iters; j++ {
				if err := applyDuplicateFieldPolicy(ev, CanonicalWins); err != nil {
					t.Errorf("applyDuplicateFieldPolicy: %v", err)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
}

// TestKeepBothUnderAttrsSingleAttrsObject verifies that a custom attr colliding
// with a canonical field is emitted under a single top-level "attrs" object.
// Regression test for MASS-0419: the policy used to rewrite the key to
// "attrs.<key>", which the dot-expanding encoder nested a second time, producing
// "attrs":{"attrs":{"<key>":...}}.
func TestKeepBothUnderAttrsSingleAttrsObject(t *testing.T) {
	sink, store := MemorySink()
	cfg := Test()
	cfg.Service = "canonical-service"
	cfg.DuplicateFieldPolicy = KeepBothUnderAttrs
	cfg.Sinks = []Sink{sink}

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{
		Event:   "checkout.request",
		Service: "canonical-service",
	})
	if err := l.Enrich(ctx, String("service", "attr-service")); err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if err := l.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	if store.Len() != 1 {
		t.Fatalf("expected 1 emitted event, got %d", store.Len())
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(store.Raw()[0]), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if got := payload["service"]; got != "canonical-service" {
		t.Fatalf("expected canonical service to be kept, got %#v", got)
	}

	attrsObj, ok := payload["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level attrs object, got %#v", payload["attrs"])
	}
	if got := attrsObj["service"]; got != "attr-service" {
		t.Fatalf("expected attrs.service == attr-service, got %#v", attrsObj["service"])
	}
	if nested, ok := attrsObj["attrs"]; ok {
		t.Fatalf("attrs must not be doubly nested; got attrs.attrs = %#v", nested)
	}
	if v, ok := attrsObj["attrs.service"]; ok {
		t.Fatalf("attr key must not retain an 'attrs.' prefix in wire output; got %#v", v)
	}
}
