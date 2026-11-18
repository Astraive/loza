package loadgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestRunCategorizesBatchOutcomes is a regression test for the always-zero
// Report.Rejected counter: a server that refuses a batch with a 4xx status
// must have those events counted as rejected rather than as generic errors.
func TestRunCategorizesBatchOutcomes(t *testing.T) {
	const (
		events    = 6
		batchSize = 2
	)

	tests := []struct {
		name         string
		status       int
		wantAccepted int64
		wantRejected int64
		wantErrors   int64
	}{
		{
			name:         "2xx responses are accepted",
			status:       http.StatusOK,
			wantAccepted: events,
		},
		{
			name:         "4xx responses are rejected, not counted as errors",
			status:       http.StatusBadRequest,
			wantRejected: events,
		},
		{
			name:       "5xx responses are errors, not rejections",
			status:     http.StatusInternalServerError,
			wantErrors: events,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			report, err := Run(context.Background(), RunnerConfig{
				URL:       srv.URL,
				Events:    events,
				Workers:   1,
				BatchSize: batchSize,
				BodySize:  16,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if report.Accepted != tt.wantAccepted {
				t.Errorf("Accepted = %d, want %d", report.Accepted, tt.wantAccepted)
			}
			if report.Rejected != tt.wantRejected {
				t.Errorf("Rejected = %d, want %d", report.Rejected, tt.wantRejected)
			}
			if report.Errors != tt.wantErrors {
				t.Errorf("Errors = %d, want %d", report.Errors, tt.wantErrors)
			}
		})
	}
}

func TestRunRejectsNonPositiveWorkers(t *testing.T) {
	for _, workers := range []int{0, -1} {
		t.Run("workers="+strconv.Itoa(workers), func(t *testing.T) {
			cfg := RunnerConfig{
				URL:       "http://127.0.0.1:1",
				Events:    10,
				Workers:   workers,
				BatchSize: 10,
				BodySize:  16,
			}

			// Before the fix this panicked with an integer divide-by-zero.
			report, err := Run(context.Background(), cfg)
			if err == nil {
				t.Fatal("expected Run to return an error for non-positive Workers")
			}
			if !strings.Contains(err.Error(), "workers") {
				t.Fatalf("expected error to mention workers, got %q", err.Error())
			}
			if report != (Report{}) {
				t.Fatalf("expected zero report on error, got %+v", report)
			}
		})
	}
}
