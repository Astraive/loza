package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

// failingSink always fails delivery, reproducing a telemetry sink outage.
type failingSink struct {
	err error
}

func (s *failingSink) Name() string { return "failing" }
func (s *failingSink) WriteEvent(context.Context, []byte, *loza.Event) error {
	return s.err
}
func (s *failingSink) Flush(context.Context) error { return nil }
func (s *failingSink) Close(context.Context) error { return nil }

func configureFailingStore(t *testing.T, err error) {
	t.Helper()
	if err := loza.Configure(loza.Test().WithSink(&failingSink{err: err})); err != nil {
		t.Fatalf("configure: %v", err)
	}
}

func TestMiddlewareReportsEmitErrorOnSuccessPath(t *testing.T) {
	wantErr := errors.New("sink unavailable")
	configureFailingStore(t, wantErr)

	var mu sync.Mutex
	var got []error
	h := MiddlewareWithConfig(Config{
		OnEmitError: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, err)
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/emit-error", nil)
	h.ServeHTTP(rr, req)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly one emit error report, got %d", len(got))
	}
	if !errors.Is(got[0], wantErr) {
		t.Fatalf("expected emit error %v, got %v", wantErr, got[0])
	}
}

func TestMiddlewareReportsEmitErrorOnPanicPath(t *testing.T) {
	wantErr := errors.New("sink unavailable")
	configureFailingStore(t, wantErr)

	var mu sync.Mutex
	var got []error
	h := MiddlewareWithConfig(Config{
		OnEmitError: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, err)
		},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom-emit-error")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic-emit-error", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly one emit error report, got %d", len(got))
	}
	if !errors.Is(got[0], wantErr) {
		t.Fatalf("expected emit error %v, got %v", wantErr, got[0])
	}
}

func TestMiddlewareDoesNotReportSuccessfulEmit(t *testing.T) {
	configureMemoryStore(t)

	called := false
	h := MiddlewareWithConfig(Config{
		OnEmitError: func(error) { called = true },
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/emit-ok", nil)
	h.ServeHTTP(rr, req)

	if called {
		t.Fatalf("OnEmitError must not be called when emission succeeds")
	}
}
