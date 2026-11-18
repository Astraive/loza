package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestWorkerEventCount verifies that the requested event count is distributed
// across workers without integer-truncation loss, including the reported case
// where events is not divisible by workers and the case events < workers.
func TestWorkerEventCount(t *testing.T) {
	tests := []struct {
		name    string
		events  int
		workers int
		want    []int
	}{
		{"evenly divisible", 12, 4, []int{3, 3, 3, 3}},
		{"reported case 10/4", 10, 4, []int{3, 3, 2, 2}},
		{"events less than workers", 3, 5, []int{1, 1, 1, 0, 0}},
		{"one event", 1, 4, []int{1, 0, 0, 0}},
		{"single worker", 10, 1, []int{10}},
		{"zero events", 0, 4, []int{0, 0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]int, 0, tt.workers)
			total := 0
			for w := 0; w < tt.workers; w++ {
				n := workerEventCount(tt.events, tt.workers, w)
				got = append(got, n)
				total += n
			}

			if total != tt.events {
				t.Fatalf("sum of per-worker counts = %d, want %d (counts: %v)", total, tt.events, got)
			}
			for w := range got {
				if got[w] != tt.want[w] {
					t.Errorf("worker %d count = %d, want %d (counts: %v)", w, got[w], tt.want[w], got)
				}
			}
		})
	}
}

// TestRunLoadTestDeliversAllEvents is an end-to-end regression test for the
// reported behavior: the load generator must deliver exactly the requested
// number of events even when that number is not divisible by the worker count.
func TestRunLoadTestDeliversAllEvents(t *testing.T) {
	var received atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode batch: %v", err)
		}
		received.Add(int64(len(body.Events)))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"accepted":0,"invalid":0,"rejected":0}`)
	}))
	defer srv.Close()

	cfg := Config{
		URL:       srv.URL,
		Events:    10,
		Workers:   4,
		BatchSize: 3,
		BodySize:  16,
	}

	if err := runLoadTest(cfg); err != nil {
		t.Fatalf("runLoadTest: %v", err)
	}

	if got := received.Load(); got != int64(cfg.Events) {
		t.Fatalf("delivered %d events, want %d", got, cfg.Events)
	}
}
