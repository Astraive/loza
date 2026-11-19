package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestDLQHandlersReturnErrorOnReadFailure verifies that DLQ control endpoints
// do not mask an internal read failure as a successful (HTTP 200) response.
func TestDLQHandlersReturnErrorOnReadFailure(t *testing.T) {
	missingDLQPath := filepath.Join(t.TempDir(), "missing-dlq.ndjson")

	tests := []struct {
		name    string
		handler func(*collectorState, http.ResponseWriter, *http.Request)
	}{
		{
			name:    "handleDLQList",
			handler: (*collectorState).handleDLQList,
		},
		{
			name:    "handleDLQReplayAll",
			handler: (*collectorState).handleDLQReplayAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &collectorState{
				cfg: collectorConfig{
					authEnabled: false,
					dlqPath:     missingDLQPath,
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/control/dlq", nil)
			rec := httptest.NewRecorder()

			tt.handler(state, rec, req)

			if rec.Code == http.StatusOK {
				t.Fatalf("expected an error status on DLQ read failure, got 200: %s", rec.Body.String())
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 on DLQ read failure, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if _, ok := resp["error"]; !ok {
				t.Fatalf("expected an error field in response: %s", rec.Body.String())
			}
		})
	}
}
