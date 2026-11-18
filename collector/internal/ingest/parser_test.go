package ingest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseEventsEmptyEnvelopeYieldsZeroEvents(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty events list", `{"events":[]}`},
		{"api version only", `{"api_version":"v1"}`},
		{"envelope metadata without events", `{"api_version":"v1","source":{"sdk":"loza-go","version":"1.0.0","service":"checkout"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(tt.body))
			events, err := ParseEvents(req, 1024)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("expected 0 events, got %d: %q", len(events), events)
			}
		})
	}
}

func TestParseEventsEnvelopeWithEventsUnwraps(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(`{"api_version":"v1","events":[{"event":"a"},{"event":"b"}]}`))
	events, err := ParseEvents(req, 1024)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if string(events[0]) != `{"event":"a"}` || string(events[1]) != `{"event":"b"}` {
		t.Fatalf("unexpected events: %q", events)
	}
}

func TestParseEventsPlainObjectStillSingleEvent(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(`{"event":"a"}`))
	events, err := ParseEvents(req, 1024)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(events) != 1 || string(events[0]) != `{"event":"a"}` {
		t.Fatalf("expected one plain object event, got %q", events)
	}
}
