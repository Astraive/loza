package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

type fixture struct {
	Event   string            `json:"event"`
	Outcome string            `json:"outcome"`
	Attrs   map[string]string `json:"attrs"`
}

// fixtureMismatch describes how an emitted event differs from fx. An empty
// string means the payload matches. Keeping this as a pure helper lets the
// conformance runner assert emitted payloads and lets tests prove the check
// actually detects wrong outcomes, events and attrs.
func fixtureMismatch(ev *loza.Event, fx fixture) string {
	if ev == nil {
		return "no emitted event"
	}
	for _, field := range []struct{ key, want string }{
		{"event", fx.Event},
		{"outcome", fx.Outcome},
	} {
		got, ok := ev.Get(field.key)
		if !ok || fmt.Sprint(got) != field.want {
			return fmt.Sprintf("%s: expected %q, got %v", field.key, field.want, got)
		}
	}
	for k, want := range fx.Attrs {
		got, ok := ev.Get(k)
		if !ok {
			// Canonical-named keys (e.g. status_code) are relocated under
			// "attrs." by the duplicate-field policy used in this runner.
			got, ok = ev.Get("attrs." + k)
		}
		if !ok {
			return fmt.Sprintf("attr %q missing from emitted event", k)
		}
		if value := fmt.Sprint(got); value != want {
			return fmt.Sprintf("attr %q: expected %q, got %q", k, want, value)
		}
	}
	return ""
}

func TestConformanceFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("cases", "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixture files found")
	}

	sink, store := loza.MemorySink()
	// Keep attrs whose names collide with canonical fields (e.g. status_code)
	// instead of silently dropping them, so every fixture attr can be checked.
	cfg := loza.Test().WithSink(sink).WithDuplicatePolicy(loza.KeepBothUnderAttrs)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var fx fixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}

			// Start from an empty store so each emitted payload can be matched
			// to the fixture that produced it.
			store.Clear()

			ctx := loza.StartEvent(context.Background(), loza.Params{Event: fx.Event})
			var attrs []loza.Attr
			for k, v := range fx.Attrs {
				attrs = append(attrs, loza.String(k, v))
			}
			if err := loza.Enrich(ctx, attrs...); err != nil {
				t.Fatalf("enrich: %v", err)
			}
			if err := loza.Finish(ctx, fx.Outcome); err != nil {
				t.Fatalf("finish: %v", err)
			}
			if err := loza.Emit(ctx); err != nil {
				t.Fatalf("emit: %v", err)
			}

			events := store.Events()
			if len(events) != 1 {
				t.Fatalf("expected exactly one emitted event, got %d", len(events))
			}
			if mismatch := fixtureMismatch(events[0], fx); mismatch != "" {
				t.Fatalf("emitted payload does not match fixture: %s", mismatch)
			}
		})
	}
}

// TestFixtureMismatchDetectsWrongPayload is the regression test for the
// conformance runner: it proves the payload checker rejects a wrong event,
// outcome or attribute instead of silently passing.
func TestFixtureMismatchDetectsWrongPayload(t *testing.T) {
	fx := fixture{
		Event:   "checkout.request",
		Outcome: "success",
		Attrs:   map[string]string{"tenant.id": "t-1"},
	}
	matching := &loza.Event{
		Event:   fx.Event,
		Outcome: fx.Outcome,
		Attrs:   []loza.Attr{loza.String("tenant.id", "t-1")},
	}
	if mismatch := fixtureMismatch(matching, fx); mismatch != "" {
		t.Fatalf("matching payload reported mismatch: %s", mismatch)
	}

	cases := []struct {
		name string
		ev   *loza.Event
	}{
		{"nil event", nil},
		{"wrong event", &loza.Event{Event: "other.event", Outcome: fx.Outcome, Attrs: matching.Attrs}},
		{"wrong outcome", &loza.Event{Event: fx.Event, Outcome: "error", Attrs: matching.Attrs}},
		{"wrong attr value", &loza.Event{Event: fx.Event, Outcome: fx.Outcome, Attrs: []loza.Attr{loza.String("tenant.id", "t-2")}}},
		{"missing attr", &loza.Event{Event: fx.Event, Outcome: fx.Outcome}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if mismatch := fixtureMismatch(tc.ev, fx); mismatch == "" {
				t.Fatalf("expected mismatch for %s", tc.name)
			}
		})
	}
}
