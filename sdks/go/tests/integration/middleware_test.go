package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/astraive/loza/sdks/go"
	lozahttp "github.com/astraive/loza/sdks/go/src/middleware/nethttp"
)

func TestNetHTTPMiddlewareEmitsEvent(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithService("test-service").WithSink(sink)
	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// The handler runs on the server goroutine, so report failures back to the
	// test goroutine instead of calling t.Fatalf from there.
	type handlerFailure struct {
		op  string
		err error
	}
	handlerErrs := make(chan handlerFailure, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := loza.Enrich(ctx, loza.String("user.id", "u-1")); err != nil {
			handlerErrs <- handlerFailure{op: "enrich", err: err}
			return
		}
		if err := loza.Finish(ctx, "success", loza.Int("status_code", http.StatusOK)); err != nil {
			handlerErrs <- handlerFailure{op: "finish", err: err}
			return
		}
		_, _ = io.WriteString(w, "ok")
	})

	srv := httptest.NewServer(lozahttp.Middleware(lozahttp.Config{Event: "http.request"})(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case failure := <-handlerErrs:
		t.Fatalf("middleware handler %s: %v", failure.op, failure.err)
	default:
	}

	ev := loza.ExpectEvent(t, store)
	if got, ok := ev.Get("user.id"); !ok || got != "u-1" {
		t.Fatalf("user.id = %v (present=%t), want %q", got, ok, "u-1")
	}
	if ev.Outcome != "success" {
		t.Fatalf("outcome = %q, want %q", ev.Outcome, "success")
	}
	if ev.StatusCode != http.StatusOK {
		t.Fatalf("status_code = %d, want %d", ev.StatusCode, http.StatusOK)
	}
}

// TestEnrichFinishErrorsAreSurfaced is a regression test for the finding that
// errors from Enrich/Finish were silently discarded. Both calls return errors
// on lifecycle violations; callers that check them (as the middleware test now
// does) can detect a failed enrichment or finish instead of it going unnoticed.
func TestEnrichFinishErrorsAreSurfaced(t *testing.T) {
	sink, _ := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithService("test-service").WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	ctx := loza.StartEvent(context.Background(), loza.Params{Event: "lifecycle.test"})
	if err := loza.Finish(ctx, "success"); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if err := loza.Finish(ctx, "success"); err == nil {
		t.Fatalf("expected a second Finish to report an error (event already finished)")
	}
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := loza.Enrich(ctx, loza.String("late", "value")); err == nil {
		t.Fatalf("expected Enrich after Emit to report an error (event closed)")
	}
}
