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

// cancelAfterContext is a context.Context that starts reporting cancellation
// after a fixed number of Err() calls. The runner checks ctx.Err() once per
// worker-loop iteration, so this makes it possible to cancel a worker
// deterministically while it is accumulating a batch, without relying on
// timing or sleeps.
type cancelAfterContext struct {
	context.Context
	mu    sync.Mutex
	calls int
	after int
	once  sync.Once
	done  chan struct{}
}

func newCancelAfterContext(after int) *cancelAfterContext {
	return &cancelAfterContext{
		Context: context.Background(),
		after:   after,
		done:    make(chan struct{}),
	}
}

func (c *cancelAfterContext) Err() error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call >= c.after {
		c.once.Do(func() { close(c.done) })
		return context.Canceled
	}
	return nil
}

func (c *cancelAfterContext) Done() <-chan struct{} { return c.done }

// TestRunAccountsForBatchBufferedOnCancellation is a regression test for the
// in-flight batch being silently dropped when the context is cancelled while
// events are buffered but before the flush condition is met.
func TestRunAccountsForBatchBufferedOnCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// BatchSize is larger than the number of events generated before
	// cancellation, so no flush happens inside the worker loop. With the
	// context cancelled on the 4th Err() call, the worker generates 3 events
	// and exits with all 3 still buffered in `batch`.
	const cancelAfter = 4
	const wantGenerated = cancelAfter - 1

	ctx := newCancelAfterContext(cancelAfter)
	report, err := Run(ctx, RunnerConfig{
		URL:       server.URL,
		Events:    10,
		Workers:   1,
		BatchSize: 100,
		BodySize:  8,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	accounted := report.Accepted + report.Rejected + report.Errors
	if accounted != wantGenerated {
		t.Fatalf("accounted events = %d, want %d (accepted=%d rejected=%d errors=%d): "+
			"events buffered at cancellation must not be silently dropped",
			accounted, wantGenerated, report.Accepted, report.Rejected, report.Errors)
	}
}

func TestRunAccountsForAllEventsWithoutCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	const events = 10
	report, err := Run(context.Background(), RunnerConfig{
		URL:       server.URL,
		Events:    events,
		Workers:   2,
		BatchSize: 3,
		BodySize:  8,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Accepted != events || report.Errors != 0 {
		t.Fatalf("report = %+v, want accepted=%d errors=0", report, events)
	}
}

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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunnerConfigValidateRejectsInvalidNumbers covers the divide-by-zero at
// runner.go:32 (Workers == 0) and other numeric inputs that would panic or
// produce nonsense. Each case must yield an error, never a panic.
func TestRunnerConfigValidateRejectsInvalidNumbers(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RunnerConfig
		wantErr string
	}{
		{"zero workers", RunnerConfig{Events: 10, Workers: 0, BatchSize: 1}, "workers"},
		{"negative workers", RunnerConfig{Events: 10, Workers: -1, BatchSize: 1}, "workers"},
		{"zero events", RunnerConfig{Events: 0, Workers: 1, BatchSize: 1}, "events"},
		{"negative events", RunnerConfig{Events: -1, Workers: 1, BatchSize: 1}, "events"},
		{"zero batch size", RunnerConfig{Events: 10, Workers: 1, BatchSize: 0}, "batch size"},
		{"negative body size", RunnerConfig{Events: 10, Workers: 1, BatchSize: 1, BodySize: -1}, "body size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil {
				t.Fatalf("expected an error for %+v, got nil", tt.cfg)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestRunRejectsZeroWorkers ensures Run itself guards the division, not just
// direct Validate callers.
func TestRunRejectsZeroWorkers(t *testing.T) {
	_, err := Run(context.Background(), RunnerConfig{Events: 10, Workers: 0, BatchSize: 1})
	if err == nil || !strings.Contains(err.Error(), "workers") {
		t.Fatalf("expected a workers error, got %v", err)
	}
}

func TestRunAcceptsValidConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	report, err := Run(context.Background(), RunnerConfig{
		URL:       srv.URL,
		Events:    4,
		Workers:   2,
		BatchSize: 2,
		BodySize:  16,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Accepted != 4 {
		t.Fatalf("expected 4 accepted events, got %d", report.Accepted)
	}
}
