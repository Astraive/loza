package main

import (
	"net/http"
	"testing"

	"github.com/astraive/loza/collector/internal/ingest"
)

// TestIngestStatusMixedInvalidQuarantined covers the regression in which a
// batch with no accepted events but both invalid and quarantined outcomes
// fell through the status switch and was reported as HTTP 202.
func TestIngestStatusMixedInvalidQuarantined(t *testing.T) {
	tests := []struct {
		name string
		resp ingest.Response
		want int
	}{
		{
			name: "all invalid",
			resp: ingest.Response{Invalid: 2},
			want: http.StatusBadRequest,
		},
		{
			name: "all quarantined",
			resp: ingest.Response{Quarantined: 2},
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "mixed invalid and quarantined with no accepted",
			resp: ingest.Response{Invalid: 1, Quarantined: 1},
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "mixed invalid and quarantined with several of each",
			resp: ingest.Response{Invalid: 3, Quarantined: 2},
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "rejected with no accepted",
			resp: ingest.Response{Rejected: 1},
			want: http.StatusServiceUnavailable,
		},
		{
			name: "mixed invalid quarantined and rejected with no accepted",
			resp: ingest.Response{Invalid: 1, Quarantined: 1, Rejected: 1},
			want: http.StatusServiceUnavailable,
		},
		{
			name: "accepted only",
			resp: ingest.Response{Accepted: 1},
			want: http.StatusAccepted,
		},
		{
			name: "accepted with partial failures",
			resp: ingest.Response{Accepted: 1, Invalid: 1, Quarantined: 1},
			want: http.StatusMultiStatus,
		},
		{
			name: "empty response",
			resp: ingest.Response{},
			want: http.StatusAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ingestStatus(tt.resp); got != tt.want {
				t.Fatalf("ingestStatus(%+v) = %d, want %d", tt.resp, got, tt.want)
			}
		})
	}
}
