package echo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	loza "github.com/astraive/loza/sdks/go"
	echopkg "github.com/labstack/echo/v4"
)

func configureEcho(t *testing.T, panicRecovery bool) *loza.MemorySinkStore {
	t.Helper()
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink).WithPanicRecovery(panicRecovery)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
	return store
}

func TestMiddlewareEmitsSuccessAndErrorEvents(t *testing.T) {
	store := configureEcho(t, false)
	e := echopkg.New()
	e.Use(Middleware())
	e.GET("/ok", func(c echopkg.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/error", func(echopkg.Context) error { return errors.New("handler failure") })

	for _, path := range []string{"/ok", "/error"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("events = %d, want 2", store.Len())
	}
}

func TestMiddlewareHandlesHandlerErrorOnlyOnce(t *testing.T) {
	store := configureEcho(t, false)
	e := echopkg.New()
	calls := 0
	e.HTTPErrorHandler = func(err error, c echopkg.Context) {
		calls++
		_ = c.String(http.StatusInternalServerError, err.Error())
	}
	e.Use(Middleware())
	e.GET("/error", func(echopkg.Context) error { return errors.New("handler failure") })

	req := httptest.NewRequest(http.MethodGet, "/error", nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)

	if calls != 1 {
		t.Fatalf("HTTP error handler calls = %d, want 1", calls)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	ev := store.Events()[0]
	if ev.Error == nil {
		t.Fatalf("error event missing error: %+v", ev)
	}
	if ev.StatusCode != http.StatusInternalServerError {
		t.Fatalf("error event status_code = %d, want %d", ev.StatusCode, http.StatusInternalServerError)
	}
}

func TestMiddlewareRecoversPanicAndUsesConfiguredEvent(t *testing.T) {
	store := configureEcho(t, true)
	e := echopkg.New()
	e.Use(Middleware(Config{Event: "echo.custom"}))
	e.GET("/panic", func(echopkg.Context) error { panic("boom") })
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", rr.Code)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	ev := store.Events()[0]
	if ev.Event != "echo.custom" || ev.Error == nil {
		t.Fatalf("panic event = %+v", ev)
	}
	if got := (panicErr{value: "x"}).Error(); got != "panic recovered: x" {
		t.Fatalf("panicErr = %q", got)
	}
}

// failingSink simulates a broken delivery path for a single event name while
// still accepting other events (such as the log line emitted on failure).
type failingSink struct {
	failEvent string
}

func (s *failingSink) Name() string { return "failing" }

func (s *failingSink) WriteEvent(_ context.Context, _ []byte, ev *loza.Event) error {
	if ev.Event == s.failEvent {
		return errors.New("sink write failed")
	}
	return nil
}

func (s *failingSink) Flush(context.Context) error { return nil }
func (s *failingSink) Close(context.Context) error { return nil }

// TestMiddlewareLogsEmitFailures verifies that an Emit delivery failure is
// surfaced through the package logger instead of being silently discarded.
func TestMiddlewareLogsEmitFailures(t *testing.T) {
	failing := &failingSink{failEvent: "http.request"}
	recorder, store := loza.MemorySink()
	cfg := loza.Test().WithSink(failing).WithSink(recorder)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

	e := echopkg.New()
	e.Use(Middleware())
	e.GET("/ok", func(c echopkg.Context) error { return c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rr := httptest.NewRecorder()
	e.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var logEvent *loza.Event
	for _, ev := range store.Events() {
		if ev.Event == "http.request.emit" {
			logEvent = ev
			break
		}
	}
	if logEvent == nil {
		t.Fatalf("emit failure was not logged; events = %+v", store.Events())
	}
	if logEvent.Message != "echo middleware: failed to emit event" {
		t.Fatalf("log message = %q", logEvent.Message)
	}
	if logEvent.Error == nil || logEvent.Error.Message != "sink write failed" {
		t.Fatalf("log error = %+v", logEvent.Error)
	}
}

// TestMiddlewareRePanicWithRecoveryDisabledStillEmits verifies that a started
// event is finished and emitted even when the panic is propagated to the caller
// because panic recovery is disabled (MASS-0497 / issue #721).
func TestMiddlewareRePanicWithRecoveryDisabledStillEmits(t *testing.T) {
	store := configureEcho(t, false)
	e := echopkg.New()
	e.Use(Middleware(Config{Event: "echo.norecov"}))
	e.GET("/panic", func(echopkg.Context) error { panic("boom") })
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rr := httptest.NewRecorder()

	func() {
		defer func() {
			if rec := recover(); rec != "boom" {
				t.Fatalf("propagated panic = %v, want boom", rec)
			}
		}()
		e.ServeHTTP(rr, req)
	}()

	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	ev := store.Events()[0]
	if ev.Event != "echo.norecov" || ev.Error == nil {
		t.Fatalf("panic event = %+v", ev)
	}
}
