package fiber

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	loza "github.com/astraive/loza/sdks/go"
	fiberpkg "github.com/gofiber/fiber/v2"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
)

func configureFiber(t *testing.T, panicRecovery bool) *loza.MemorySinkStore {
	t.Helper()
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink).WithPanicRecovery(panicRecovery)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
	return store
}

func TestMiddlewareEmitsSuccessAndErrorEvents(t *testing.T) {
	store := configureFiber(t, false)
	app := fiberpkg.New()
	app.Use(Middleware())
	app.Get("/ok", func(c *fiberpkg.Ctx) error { return c.SendString("ok") })
	app.Get("/error", func(*fiberpkg.Ctx) error { return errors.New("handler failure") })
	for _, path := range []string{"/ok", "/error"} {
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if _, err := app.Test(req); err != nil {
			t.Fatalf("app.Test %s: %v", path, err)
		}
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("events = %d, want 2", store.Len())
	}
}

// TestMiddlewareRecordedStatusMatchesServedStatus guards against recording the
// handler's snapshot status when Fiber's error handler will overwrite it with a
// different value before the response is sent.
func TestMiddlewareRecordedStatusMatchesServedStatus(t *testing.T) {
	tests := []struct {
		name    string
		handler fiberpkg.Handler
		want    int
	}{
		{
			name: "plain error after setting a status is served as 500",
			handler: func(c *fiberpkg.Ctx) error {
				c.Status(http.StatusNotFound)
				return errors.New("handler failure")
			},
			want: http.StatusInternalServerError,
		},
		{
			name: "fiber error is served with its own code",
			handler: func(*fiberpkg.Ctx) error {
				return fiberpkg.NewError(http.StatusTeapot, "teapot")
			},
			want: http.StatusTeapot,
		},
		{
			name: "successful handler keeps its status",
			handler: func(c *fiberpkg.Ctx) error {
				return c.Status(http.StatusAccepted).SendString("accepted")
			},
			want: http.StatusAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := configureFiber(t, false)
			app := fiberpkg.New()
			app.Use(Middleware())
			app.Get("/case", tt.handler)

			req, err := http.NewRequest(http.MethodGet, "/case", nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			if resp.StatusCode != tt.want {
				t.Fatalf("served status = %d, want %d", resp.StatusCode, tt.want)
			}
			if err := loza.Flush(context.Background()); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if store.Len() != 1 {
				t.Fatalf("events = %d, want 1", store.Len())
			}
			if got := store.Events()[0].StatusCode; got != tt.want {
				t.Fatalf("recorded status_code = %d, want %d (status actually served)", got, tt.want)
			}
		})
	}
}

func TestMiddlewareRecoversPanicAndAddsRoute(t *testing.T) {
	store := configureFiber(t, true)
	app := fiberpkg.New()
	app.Use(Middleware(Config{Event: "fiber.custom"}))
	app.Get("/panic", func(*fiberpkg.Ctx) error { panic("boom") })
	req, err := http.NewRequest(http.MethodGet, "/panic", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", resp.StatusCode)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	ev := store.Events()[0]
	if ev.Event != "fiber.custom" || ev.Error == nil {
		t.Fatalf("panic event = %+v", ev)
	}
	if ev.Route != "/panic" {
		t.Fatalf("panic event route = %q, want %q", ev.Route, "/panic")
	}
	if got := (panicErr{value: "x"}).Error(); got != "panic recovered: x" {
		t.Fatalf("panicErr = %q", got)
	}
}

// TestMiddlewarePanicValueNotEchoedToClient guards against disclosing the raw
// recovered panic value through Fiber's error handler, which writes err.Error()
// into the response body. The detail must remain in telemetry only.
func TestMiddlewarePanicValueNotEchoedToClient(t *testing.T) {
	const secret = "s3cr3t-panic-detail"
	store := configureFiber(t, true)

	errorHandlerCalled := false
	app := fiberpkg.New(fiberpkg.Config{
		ErrorHandler: func(c *fiberpkg.Ctx, err error) error {
			errorHandlerCalled = true
			return c.Status(fiberpkg.StatusInternalServerError).SendString(err.Error())
		},
	})
	app.Use(Middleware())
	app.Get("/panic", func(*fiberpkg.Ctx) error { panic(secret) })
	req, err := http.NewRequest(http.MethodGet, "/panic", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", resp.StatusCode)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("panic value disclosed to client in body: %q", body)
	}
	if errorHandlerCalled {
		t.Fatal("Fiber error handler was invoked with the panic error")
	}

	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	ev := store.Events()[0]
	if ev.Error == nil || !strings.Contains(ev.Error.Message, secret) {
		t.Fatalf("telemetry lost panic detail: %+v", ev.Error)
	}
}

// A panic after the handler set a 4xx status must still record the status that
// Fiber serves (500), not the stale handler snapshot.
func TestMiddlewarePanicRecordsServedStatus(t *testing.T) {
	store := configureFiber(t, true)
	app := fiberpkg.New()
	app.Use(Middleware())
	app.Get("/panic", func(c *fiberpkg.Ctx) error {
		c.Status(http.StatusNotFound)
		panic("boom")
	})
// TestMiddlewareRePanicWithRecoveryDisabledStillEmits verifies that a started
// event is finished and emitted even when the panic is propagated to the caller
// because panic recovery is disabled (MASS-0497 / issue #721).
func TestMiddlewareRePanicWithRecoveryDisabledStillEmits(t *testing.T) {
	store := configureFiber(t, false)
	app := fiberpkg.New()
	// A server-level recover outside the middleware catches the propagated
	// panic so app.Test does not crash the test process.
	app.Use(fiberrecover.New())
	app.Use(Middleware(Config{Event: "fiber.norecov"}))
	app.Get("/panic", func(*fiberpkg.Ctx) error { panic("boom") })
	req, err := http.NewRequest(http.MethodGet, "/panic", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("served status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("events = %d, want 1", store.Len())
	}
	if got := store.Events()[0].StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("recorded status_code = %d, want %d (status actually served)", got, http.StatusInternalServerError)
	ev := store.Events()[0]
	if ev.Event != "fiber.norecov" || ev.Error == nil {
		t.Fatalf("panic event = %+v", ev)
	}
}
