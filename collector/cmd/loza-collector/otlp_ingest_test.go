package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// failingReader simulates an error while the request body is being read.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// errorDedupeStore simulates a failing dedupe backend. The wrapped error is
// the kind of internal detail that must never be reflected to an OTLP client.
type errorDedupeStore struct{ err error }

func (s errorDedupeStore) SeenBefore(context.Context, string, time.Duration) (bool, error) {
	return false, s.err
}
func (s errorDedupeStore) Close() error { return nil }

func captureCollectorLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

func otlpErrorResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, rec.Body.String())
	}
	return out
}

func TestHandleOTLPLogsDoesNotReflectInternalDetails(t *testing.T) {
	const secret = "internal-host:6379 password=supersecret"

	tests := []struct {
		name        string
		body        *bytes.Reader
		bodyErr     error
		contentType string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "body read failure",
			bodyErr:     errors.New(secret),
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_otlp_request",
			wantMessage: "failed to read OTLP request body",
		},
		{
			name:        "protobuf decode failure",
			body:        bytes.NewReader([]byte{0xff, 0xff, 0xff}),
			contentType: "application/x-protobuf",
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_otlp_protobuf",
			wantMessage: "invalid OTLP protobuf payload",
		},
		{
			name:        "json decode failure",
			body:        bytes.NewReader([]byte(`{"resourceLogs":[`)),
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_otlp_json",
			wantMessage: "invalid OTLP JSON payload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &collectorState{
				cfg:         testCollectorConfig(),
				ingestSink:  &fakeSink{},
				rateLimiter: rate.NewLimiter(rate.Limit(1000), 1000),
			}
			state.ready.Store(true)

			var bodyReader io.Reader = tc.body
			if tc.bodyErr != nil {
				bodyReader = failingReader{err: tc.bodyErr}
			}
			req := httptest.NewRequest(http.MethodPost, "/otlp/logs", bodyReader)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()

			state.handleOTLPLogs(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			resp := otlpErrorResponse(t, rec)
			if got, _ := resp["error"].(string); got != tc.wantCode {
				t.Fatalf("expected error code %q, got %q", tc.wantCode, got)
			}
			if got, _ := resp["message"].(string); got != tc.wantMessage {
				t.Fatalf("expected stable message %q, got %q", tc.wantMessage, got)
			}
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("response reflected internal detail: %s", rec.Body.String())
			}
		})
	}
}

func TestHandleOTLPLogsIngestFailureDoesNotReflectInternalDetails(t *testing.T) {
	const internalErr = "dedupe unavailable: connect redis://internal-host:6379 password=supersecret"

	logs := captureCollectorLogs(t)

	cfg := testCollectorConfig()
	cfg.dedupeEnabled = true
	cfg.dedupeKey = "event_id"
	cfg.dedupeWindow = time.Minute
	cfg.processorRegistry = []string{"dedupe"}

	state := &collectorState{
		cfg:          cfg,
		ingestSink:   &fakeSink{},
		rateLimiter:  rate.NewLimiter(rate.Limit(1000), 1000),
		dedupeSeenAt: make(map[string]time.Time),
		dedupeStore:  errorDedupeStore{err: errors.New(internalErr)},
	}
	state.ready.Store(true)

	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"severityText":"INFO","body":{"stringValue":"payment.completed"}}]}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/otlp/logs", strings.NewReader(body))
	rec := httptest.NewRecorder()

	state.handleOTLPLogs(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := otlpErrorResponse(t, rec)
	if got, _ := resp["error"].(string); got != "otlp_ingest_failed" {
		t.Fatalf("expected error code otlp_ingest_failed, got %q", got)
	}
	const wantMessage = "OTLP ingestion temporarily unavailable"
	if got, _ := resp["message"].(string); got != wantMessage {
		t.Fatalf("expected stable message %q, got %q", wantMessage, got)
	}
	if strings.Contains(rec.Body.String(), internalErr) || strings.Contains(rec.Body.String(), "internal-host") || strings.Contains(rec.Body.String(), "supersecret") {
		t.Fatalf("response reflected internal ingest error: %s", rec.Body.String())
	}

	// The detailed error must still be available server-side for operators.
	if !strings.Contains(logs.String(), internalErr) {
		t.Fatalf("expected detailed error to be logged server-side, got logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "collector_otlp_ingest_failed") {
		t.Fatalf("expected server-side log event collector_otlp_ingest_failed, got logs: %s", logs.String())
	}
}
