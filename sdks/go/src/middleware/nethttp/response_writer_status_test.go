package nethttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseWriterWriteHeaderFirstCallWins(t *testing.T) {
	wrapped, state := newResponseWriter(newBaseTestWriter())
	wrapped.WriteHeader(http.StatusAccepted)
	wrapped.WriteHeader(http.StatusInternalServerError)

	if state.statusCode != http.StatusAccepted {
		t.Fatalf("tracked status after repeated WriteHeader = %d, want %d", state.statusCode, http.StatusAccepted)
	}
	if !state.wroteHeader {
		t.Fatal("expected wroteHeader to be true after WriteHeader")
	}
}

func TestResponseWriterWriteHeaderAfterImplicitWriteIsIgnored(t *testing.T) {
	wrapped, state := newResponseWriter(newBaseTestWriter())
	if _, err := wrapped.Write([]byte("body")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	wrapped.WriteHeader(http.StatusInternalServerError)

	if state.statusCode != http.StatusOK {
		t.Fatalf("tracked status after WriteHeader following Write = %d, want %d", state.statusCode, http.StatusOK)
	}
}

func TestMiddlewareRepeatedWriteHeaderLogsFirstStatus(t *testing.T) {
	store := configureMemoryStore(t)
	h := Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/repeated-header", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("response status = %d, want %d", rr.Code, http.StatusAccepted)
	}
	ev := singleEvent(t, store)
	if ev.StatusCode != http.StatusAccepted {
		t.Fatalf("event status_code = %d, want %d (first WriteHeader wins)", ev.StatusCode, http.StatusAccepted)
	}
}
