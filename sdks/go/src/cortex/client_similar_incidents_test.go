package cortex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientSimilarIncidentsValidatesReconstructResponse is a regression test
// for MASS-0468. SimilarIncidents decodes the /reconstruct response into an
// IncidentContext just like Reconstruct does, so it must run the same
// normalize/validate sequence instead of returning a malformed payload silently.
func TestClientSimilarIncidentsValidatesReconstructResponse(t *testing.T) {
	tests := []struct {
		name    string
		context IncidentContext
		wantErr bool
	}{
		{
			name: "valid response returns similar incidents",
			context: IncidentContext{
				IncidentID:       "inc-1",
				Timestamp:        "2026-01-01T00:00:00Z",
				Confidence:       0.9,
				SimilarIncidents: []map[string]any{{"id": "similar"}},
			},
		},
		{
			name: "missing incident_id is rejected like Reconstruct",
			context: IncidentContext{
				Timestamp:        "2026-01-01T00:00:00Z",
				SimilarIncidents: []map[string]any{{"id": "similar"}},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.context)
			}))
			defer srv.Close()

			c := NewClient(srv.URL)
			similar, err := c.SimilarIncidents(context.Background(), "inc-1")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SimilarIncidents accepted malformed /reconstruct response: %#v", similar)
				}
				if len(similar) != 0 {
					t.Fatalf("SimilarIncidents returned results alongside validation error: %#v", similar)
				}
				return
			}
			if err != nil {
				t.Fatalf("SimilarIncidents: %v", err)
			}
			if len(similar) != 1 {
				t.Fatalf("expected 1 similar incident, got %#v", similar)
			}
		})
	}
}
