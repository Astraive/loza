package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPostIngestValidatesEnvelopeBeforeSending(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	payload := []byte(`{"api_version":"v1","source":{"sdk":"loza-cli","version":"1.0.0","service":"checkout"}}`)
	if err := PostIngest(srv.URL, "application/json", payload); err == nil {
		t.Fatal("expected invalid envelope to fail validation")
	}
	if hits.Load() != 0 {
		t.Fatalf("expected no request to be sent, got %d hits", hits.Load())
	}
}

func TestPostIngestRetriesRetryableCollectorResponse(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Method, http.MethodPost) {
			t.Fatalf("unexpected method %s", r.Method)
		}
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"status":"partial","accepted":0,"rejected":0,"invalid":0,"errors":[{"code":"rate_limited","message":"retry","retryable":true}]}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted","accepted":1,"rejected":0,"invalid":0}`))
	}))
	defer srv.Close()

	payload := []byte(`{"api_version":"v1","source":{"sdk":"loza-cli","version":"1.0.0","service":"checkout"},"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}]}`)
	if err := PostIngest(srv.URL, "application/json", payload); err != nil {
		t.Fatalf("expected retryable ingest to succeed, got %v", err)
	}
	if hits.Load() < 2 {
		t.Fatalf("expected retry to happen, hits=%d", hits.Load())
	}
}

func TestPostIngestExhaustedRetriesReturnWrappedError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":"partial","accepted":0,"rejected":0,"invalid":0,"errors":[{"code":"rate_limited","message":"still busy","retryable":true}]}`))
	}))
	defer srv.Close()

	payload := []byte(`{"api_version":"v1","source":{"sdk":"loza-cli","version":"1.0.0","service":"checkout"},"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}]}`)
	err := PostIngest(srv.URL, "application/json", payload)
	if err == nil {
		t.Fatal("expected exhausted retries to fail")
	}
	if !strings.Contains(err.Error(), "collector send failed") {
		t.Fatalf("expected wrapped exhausted-retry error, got %v", err)
	}
	if !strings.Contains(err.Error(), "still busy") {
		t.Fatalf("expected wrapped error to retain collector reason, got %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("expected 3 attempts before giving up, got %d", hits.Load())
	}
}

func TestPostIngestExhaustedNetworkErrorsReturnWrappedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	payload := []byte(`{"api_version":"v1","source":{"sdk":"loza-cli","version":"1.0.0","service":"checkout"},"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}]}`)
	err := PostIngest(url, "application/json", payload)
	if err == nil {
		t.Fatal("expected exhausted network retries to fail")
	}
	if !strings.Contains(err.Error(), "collector send failed") {
		t.Fatalf("expected wrapped exhausted-retry error, got %v", err)
	}
}

func TestTestSinkPostsToCollectorEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/sinks/duckdb/test" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected application/json content type, got %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if strings.TrimSpace(string(body)) != "{}" {
			t.Fatalf("expected default JSON body, got %q", string(body))
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	resp, err := TestSink(context.Background(), srv.URL, "duckdb")
	if err != nil {
		t.Fatalf("test sink: %v", err)
	}
	if !strings.Contains(string(resp), `"status":"ok"`) {
		t.Fatalf("unexpected response: %s", string(resp))
	}
}

func TestWatchStreamEscapesFilterParameters(t *testing.T) {
	queries := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tail" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		queries <- r.URL.Query()
		_, _ = w.Write([]byte(""))
	}))
	defer srv.Close()

	filters := map[string]string{
		"service":   "a&debug=1",
		"level=x&y": "info",
	}
	conn, err := WatchStream(context.Background(), srv.URL, filters)
	if err != nil {
		t.Fatalf("watch stream: %v", err)
	}
	defer conn.Close()

	got := <-queries
	if len(got) != len(filters) {
		t.Fatalf("expected exactly %d query params, got %d: %v", len(filters), len(got), got)
	}
	if v := got.Get("service"); v != "a&debug=1" {
		t.Fatalf("service param = %q, want %q", v, "a&debug=1")
	}
	if _, ok := got["debug"]; ok {
		t.Fatalf("query parameter injection: unexpected %q param in %v", "debug", got)
	}
	if _, ok := got["y"]; ok {
		t.Fatalf("query parameter injection: unexpected %q param in %v", "y", got)
	}
	if v := got.Get("level=x&y"); v != "info" {
		t.Fatalf("escaped key param = %q, want %q", v, "info")
	}
}
