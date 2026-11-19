package loadgen

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestRunRejectsNonPositiveBatchSize(t *testing.T) {
	for _, batchSize := range []int{-1, 0} {
		t.Run(fmt.Sprintf("batchsize_%d", batchSize), func(t *testing.T) {
			cfg := RunnerConfig{
				URL:       "http://127.0.0.1:9308",
				Events:    10,
				Workers:   2,
				BatchSize: batchSize,
				BodySize:  16,
			}
			_, err := Run(context.Background(), cfg)
			if err == nil {
				t.Fatalf("Run with batch size %d: expected an error, got nil", batchSize)
			}
			if !strings.Contains(err.Error(), "batch size") {
				t.Fatalf("Run with batch size %d: expected a batch size error, got %q", batchSize, err)
			}
		})
	}
}

func TestRunBatchesEventsWithPositiveBatchSize(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := RunnerConfig{
		URL:       srv.URL,
		Events:    10,
		Workers:   1,
		BatchSize: 5,
		BodySize:  16,
	}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run with valid batch size: %v", err)
	}
	if report.Accepted != 10 {
		t.Fatalf("expected 10 accepted events, got %d", report.Accepted)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("expected 2 batched requests, got %d", requests)
	}
}
