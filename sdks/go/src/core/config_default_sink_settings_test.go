package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The default collector sink built by NewClient must honor the configured
// retry and backoff settings instead of silently ignoring them.
func TestNewClientDefaultCollectorSinkHonorsMaxRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	logger, err := NewClient(ApplyConfig(Config{},
		WithCollectorURL(srv.URL),
		WithService("retry-test"),
		WithMaxRetries(2),
		WithMaxBackoff(5*time.Millisecond),
		WithCompression(false),
		WithAsync(false),
	))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := logger.StartEvent(context.Background(), Params{Event: "default.sink.retry"})
	if err := logger.Finish(ctx, "success"); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := logger.Emit(ctx); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if err := logger.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Fatalf("HTTP attempts = %d, want 3 (initial request + 2 configured retries)", got)
	}
	if got := logger.cfg.Sinks[0].Name(); got != "httpbatch" {
		t.Fatalf("default sink = %q, want httpbatch", got)
	}
}

// The configured request timeout must be applied to the default collector
// sink's HTTP client rather than the previous hard-coded 5s timeout.
func TestNewClientDefaultCollectorSinkHonorsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	logger, err := NewClient(ApplyConfig(Config{},
		WithCollectorURL(srv.URL),
		WithService("timeout-test"),
		WithTimeout(50*time.Millisecond),
		WithMaxBackoff(time.Millisecond),
		WithCompression(false),
		WithAsync(false),
	))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := logger.StartEvent(context.Background(), Params{Event: "default.sink.timeout"})
	if err := logger.Finish(ctx, "success"); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := logger.Emit(ctx); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	err = logger.Flush(context.Background())
	if err == nil {
		t.Fatal("Flush() succeeded; configured request timeout was ignored")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "timeout") && !strings.Contains(msg, "deadline") {
		t.Fatalf("Flush() error = %v, want a timeout/deadline error", err)
	}
}
