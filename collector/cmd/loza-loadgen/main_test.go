package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "valid config", cfg: Config{Workers: 4, Events: 10000}, wantErr: false},
		{name: "zero events is allowed", cfg: Config{Workers: 4, Events: 0}, wantErr: false},
		{name: "zero workers", cfg: Config{Workers: 0, Events: 10000}, wantErr: true},
		{name: "negative workers", cfg: Config{Workers: -1, Events: 10000}, wantErr: true},
		{name: "negative events", cfg: Config{Workers: 4, Events: -1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateConfig(%+v) error = %v, wantErr %v", tt.cfg, err, tt.wantErr)
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

// A malformed URL makes http.NewRequestWithContext return a nil request and a
// non-nil error. sendBatch must not dereference that nil request and must
// account for the failed batch in stats.errors instead of panicking.
func TestSendBatchMalformedURL(t *testing.T) {
	stats := &Stats{}
	cfg := Config{URL: "://malformed", BatchSize: 2}
	batch := [][]byte{[]byte(`{"event":"a"}`), []byte(`{"event":"b"}`)}

	sendBatch(context.Background(), &http.Client{}, cfg, batch, stats)

	if got := atomic.LoadInt64(&stats.errors); got != int64(len(batch)) {
		t.Fatalf("stats.errors = %d, want %d", got, len(batch))
	}
	if got := atomic.LoadInt64(&stats.accepted); got != 0 {
		t.Fatalf("stats.accepted = %d, want 0", got)
	}
	if got := atomic.LoadInt64(&stats.invalid); got != 0 {
		t.Fatalf("stats.invalid = %d, want 0", got)
	}
	if got := atomic.LoadInt64(&stats.rejected); got != 0 {
		t.Fatalf("stats.rejected = %d, want 0", got)
	}
}

// Reproduce the reported trigger end to end: a malformed -url must not panic
// in run()/runLoadTest()/sendBatch().
func TestRunMalformedURLDoesNotPanic(t *testing.T) {
	if err := run([]string{"-url", "://malformed", "-events", "1", "-workers", "1", "-batch", "1"}); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}
}

// Guard that well-formed requests still set headers and are counted normally.
func TestSendBatchValidURL(t *testing.T) {
	contentTypes := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes <- r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":1,"invalid":0,"rejected":0}`))
	}))
	defer srv.Close()

	stats := &Stats{}
	cfg := Config{URL: srv.URL, APIKey: "test-key"}
	sendBatch(context.Background(), srv.Client(), cfg, [][]byte{[]byte(`{"event":"a"}`)}, stats)

	if got := <-contentTypes; got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := atomic.LoadInt64(&stats.accepted); got != 1 {
		t.Fatalf("stats.accepted = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&stats.errors); got != 0 {
		t.Fatalf("stats.errors = %d, want 0", got)
	}
}

// TestRunRejectsInvalidFlags reproduces the reported behavior: invalid
// -workers/-events flags used to reach cfg.Events/cfg.Workers and panic with a
// division by zero before any request was sent. run must instead return an
// error.
func TestRunRejectsInvalidFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero workers", args: []string{"-workers", "0"}, want: "workers"},
		{name: "negative workers", args: []string{"-workers", "-1"}, want: "workers"},
		{name: "negative events", args: []string{"-events", "-1"}, want: "events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if err == nil {
				t.Fatalf("run(%v) expected an error for invalid flags, got nil", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run(%v) error = %q, want it to mention %q", tt.args, err, tt.want)
			}
		})
	}
}

// TestRunReturnsErrorOnParseFailure reproduces the reported defect: with
// flag.ContinueOnError, malformed input used to be silently discarded and the
// load test would run with unintended defaults (10,000 events) instead of
// failing fast.
func TestRunReturnsErrorOnParseFailure(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "non-numeric events", args: []string{"-events", "abc"}},
		{name: "non-numeric workers", args: []string{"-workers", "nope"}},
		{name: "non-numeric batch", args: []string{"-batch", "x"}},
		{name: "non-numeric body size", args: []string{"-body-size", "large"}},
		{name: "unknown flag", args: []string{"-does-not-exist"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(tt.args); err == nil {
				t.Fatalf("run(%v) returned nil, want a flag parsing error", tt.args)
			}
		})
	}
}

// TestRunAcceptsValidFlags guards against the fix rejecting well-formed
// input. Zero events keeps the run network-free and fast.
func TestRunAcceptsValidFlags(t *testing.T) {
	if err := run([]string{"-events", "0", "-workers", "1"}); err != nil {
		t.Fatalf("run with valid flags returned error: %v", err)
	}
}

// TestParseErrorMentionsBadValue checks that the underlying flag error is
// propagated rather than replaced with a generic message.
func TestParseErrorMentionsBadValue(t *testing.T) {
	err := run([]string{"-events", "abc"})
	if err == nil {
		t.Fatal("run(-events abc) returned nil, want a flag parsing error")
	}
	if !strings.Contains(err.Error(), "events") {
		t.Fatalf("error %q does not identify the offending flag", err)
	}
}
