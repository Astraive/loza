package core

import (
	"testing"
	"time"
)

func TestFlatSchemaMapPreservesCanonicalFieldsOnAttrCollision(t *testing.T) {
	ev := &Event{
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EventID:   "evt-1",
		Level:     LevelWarn,
		Event:     "checkout.request",
		Message:   "canonical message",
		Attrs: []Attr{
			String("level", "attr-level"),
			String("message", "attr message"),
			Group("user", String("id", "u-1")),
		},
	}

	flat := flatSchemaMap(newEventView(ev))

	for field, want := range map[string]any{
		"level":    "warn",
		"message":  "canonical message",
		"event":    "checkout.request",
		"event_id": "evt-1",
	} {
		got, ok := flat[field]
		if !ok {
			t.Errorf("flat schema missing canonical field %q", field)
			continue
		}
		if got != want {
			t.Errorf("flat schema canonical field %q = %v, want %v", field, got, want)
		}
	}

	// The colliding attr values are not dropped: they remain available under
	// the canonical "attrs" object.
	attrs, _ := flat["attrs"].(map[string]any)
	if attrs["level"] != "attr-level" || attrs["message"] != "attr message" {
		t.Errorf("flat schema lost colliding attrs: %#v", flat["attrs"])
	}

	// Non-colliding attrs are still flattened.
	if got := flat["user_id"]; got != "u-1" {
		t.Errorf("flat schema user_id = %v, want u-1", got)
	}
}

func TestFlatSchemaMapPreservesCanonicalFieldWhenAttrGroupCollides(t *testing.T) {
	ev := &Event{
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EventID:   "evt-2",
		Level:     LevelError,
		Attrs: []Attr{
			Group("level", String("code", "sev-3")),
		},
	}

	flat := flatSchemaMap(newEventView(ev))

	if got := flat["level"]; got != "error" {
		t.Errorf("flat schema canonical level = %v, want error", got)
	}
	if got := flat["level_code"]; got != "sev-3" {
		t.Errorf("flat schema level_code = %v, want sev-3", got)
	}
}
