package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleReplayRejectsUnsupportedFilter guards against the replay endpoint
// accepting a "filter" field and silently discarding it. Since replay has no
// filter semantics, any non-empty filter must be rejected instead of ignored.
func TestHandleReplayRejectsUnsupportedFilter(t *testing.T) {
	state := newTestCollectorState()

	body := []byte(`{"events":[[123]],"filter":"service=checkout"}`)
	req := httptest.NewRequest(http.MethodPost, "/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	state.handleReplay(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported filter, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "unsupported_filter" {
		t.Fatalf("error = %v, want %q", resp["error"], "unsupported_filter")
	}
}

// TestHandleReplayAllowsEmptyFilter confirms requests without a meaningful
// filter are not rejected by the new validation and still reach event checks.
func TestHandleReplayAllowsEmptyFilter(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "absent filter", body: `{"events":[]}`},
		{name: "explicit empty filter", body: `{"events":[],"filter":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := newTestCollectorState()
			req := httptest.NewRequest(http.MethodPost, "/replay", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			state.handleReplay(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp["error"] != "empty_events" {
				t.Fatalf("error = %v, want %q (filter validation must not reject)", resp["error"], "empty_events")
			}
		})
	}
}
