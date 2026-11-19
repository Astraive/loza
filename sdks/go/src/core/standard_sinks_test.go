package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRotatingFileSinkRotateIncludesReopenError(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "existing.log")
	f, err := os.OpenFile(existingPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open test file: %v", err)
	}

	s := &rotatingFileSink{
		cfg: RotatingFileConfig{
			Path: filepath.Join(dir, "missing", "current.log"),
		},
		f: f,
	}

	err = s.rotate()
	if err == nil {
		t.Fatalf("expected rotate error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "rotate") {
		t.Fatalf("expected rotate error context, got %q", msg)
	}
	if !strings.Contains(msg, "reopen") {
		t.Fatalf("expected reopen error context, got %q", msg)
	}
}

func TestRotatingFileSinkRotationsWithinSameSecondDoNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	s, err := RotatingFileSink(RotatingFileConfig{
		Path:     path,
		MaxBytes: 1024 * 1024,
		MaxAge:   time.Hour,
	})
	if err != nil {
		t.Fatalf("RotatingFileSink() error = %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()

	rs := s.(*rotatingFileSink)
	writes := []string{"first\n", "second\n", "third\n"}
	for _, content := range writes {
		if _, err := rs.f.WriteString(content); err != nil {
			t.Fatalf("write %q: %v", content, err)
		}
		// Consecutive rotations land in the same second; each must produce a
		// distinct rotated file rather than overwriting the previous one.
		if err := rs.rotate(); err != nil {
			t.Fatalf("rotate() error = %v", err)
		}
	}

	matches, err := filepath.Glob(filepath.Join(dir, "app.log*"))
	if err != nil {
		t.Fatalf("glob rotated files: %v", err)
	}
	if len(matches) < len(writes) {
		t.Fatalf("expected at least %d distinct log files, got %d: %v", len(writes), len(matches), matches)
	}

	contents := make(map[string]string, len(matches))
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			t.Fatalf("read %q: %v", match, err)
		}
		contents[match] = string(data)
	}
	for _, want := range writes {
		found := 0
		for _, data := range contents {
			found += strings.Count(data, want)
		}
		if found != 1 {
			t.Fatalf("content %q found %d times across %v; log data was lost or duplicated", want, found, contents)
		}
	}
}

func TestCollectorSink_RetriesRetryableResponse(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"code":"rate_limited","message":"retry","retryable":true}]}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	sink, err := CollectorSink(CollectorSinkConfig{
		Endpoint:          srv.URL,
		MaxRetries:        2,
		MaxBackoff:        100 * time.Millisecond,
		Timeout:           2 * time.Second,
		ConnectionTimeout: 2 * time.Second,
		Service:           "svc",
	})
	if err != nil {
		t.Fatalf("CollectorSink() error = %v", err)
	}

	ev := &Event{Service: "svc", Event: "test.event"}
	encoded := []byte(`{"event_id":"evt_1","event":"test.event","service":"svc"}`)
	if err := sink.WriteEvent(context.Background(), encoded, ev); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected retry to happen, attempts=%d", attempts.Load())
	}
}

func TestCollectorSink_UsesCustomClientWithRetries(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			n := attempts.Add(1)
			if n == 1 {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Retry-After": []string{"0"}},
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader(`{"status":"accepted"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	sink, err := CollectorSink(CollectorSinkConfig{
		Endpoint:          "https://collector.example",
		Client:            client,
		MaxRetries:        1,
		MaxBackoff:        10 * time.Millisecond,
		Timeout:           2 * time.Second,
		ConnectionTimeout: 2 * time.Second,
		Service:           "svc",
	})
	if err != nil {
		t.Fatalf("CollectorSink() error = %v", err)
	}

	ev := &Event{Service: "svc", Event: "test.event"}
	encoded := []byte(`{"event_id":"evt_1","event":"test.event","service":"svc"}`)
	if err := sink.WriteEvent(context.Background(), encoded, ev); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected custom client retries, attempts=%d", attempts.Load())
	}
}

func TestCollectorSink_FailsOnPartialInvalidBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"status":"partial","accepted":1,"invalid":1,"acks":[{"status":"accepted","reason":"accepted"},{"status":"invalid","reason":"schema_invalid","message":"schema validation failed"}]}`))
	}))
	defer srv.Close()

	sink, err := CollectorSink(CollectorSinkConfig{
		Endpoint:          srv.URL,
		MaxRetries:        0,
		MaxBackoff:        10 * time.Millisecond,
		Timeout:           2 * time.Second,
		ConnectionTimeout: 2 * time.Second,
		Service:           "svc",
	})
	if err != nil {
		t.Fatalf("CollectorSink() error = %v", err)
	}

	ev := &Event{Service: "svc", Event: "test.event"}
	encoded := []byte(`{"event_id":"evt_1","event":"test.event","service":"svc"}`)
	err = sink.WriteEvent(context.Background(), encoded, ev)
	if err == nil {
		t.Fatalf("expected partial invalid batch to fail")
	}
	if !strings.Contains(err.Error(), "schema validation failed") {
		t.Fatalf("expected schema failure in error, got %v", err)
	}
}

func TestCollectorSink_CompressesRequestBodyWhenEnabled(t *testing.T) {
	var contentEncoding string
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentEncoding = r.Header.Get("Content-Encoding")
		var err error
		rawBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	sink, err := CollectorSink(CollectorSinkConfig{
		Endpoint:          srv.URL,
		MaxRetries:        0,
		MaxBackoff:        10 * time.Millisecond,
		Timeout:           2 * time.Second,
		ConnectionTimeout: 2 * time.Second,
		Service:           "svc",
		EnableCompression: true,
	})
	if err != nil {
		t.Fatalf("CollectorSink() error = %v", err)
	}

	ev := &Event{Service: "svc", Event: "test.event"}
	encoded := []byte(`{"event_id":"evt_1","event":"test.event","service":"svc"}`)
	if err := sink.WriteEvent(context.Background(), encoded, ev); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}

	if contentEncoding != "gzip" {
		t.Fatalf("expected gzip content-encoding, got %q", contentEncoding)
	}
	reader, err := gzip.NewReader(bytes.NewReader(rawBody))
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Contains(decompressed, []byte(`"events"`)) {
		t.Fatalf("expected compressed collector envelope, got %s", string(decompressed))
	}
}

func TestHTTPBatchSink_BasicAuthAndSafeEndpoint(t *testing.T) {
	var gotUser, gotPassword string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, _ = r.BasicAuth()
		if strings.Contains(r.URL.String(), "dsn-secret") {
			t.Errorf("request URL contains credential material: %q", r.URL.String())
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	endpoint := srv.URL + "/events"
	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      endpoint,
		BasicUsername: "dsn-user",
		BasicPassword: "dsn-secret",
		Insecure:      true,
		BatchSize:     1,
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink() error = %v", err)
	}
	defer sink.Close(context.Background())
	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if gotUser != "dsn-user" || gotPassword != "dsn-secret" {
		t.Fatalf("BasicAuth = %q/%q, want DSN credentials", gotUser, gotPassword)
	}
}

func TestHTTPBatchSink_PublicBasicAuthUsesEmptyPassword(t *testing.T) {
	const capability = "lz_pub_6DJvd3D0izOaQx3n5BhKqN"
	var gotUser, gotPassword, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, _ = r.BasicAuth()
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      srv.URL + "/collectors/public-collector/events",
		BasicUsername: capability,
		BasicPassword: "",
		Insecure:      true,
		BatchSize:     1,
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink() error = %v", err)
	}
	defer sink.Close(context.Background())
	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if gotUser != capability || gotPassword != "" {
		t.Fatalf("BasicAuth = %q/%q, want public capability with empty password", gotUser, gotPassword)
	}
	if gotPath != "/collectors/public-collector/events" {
		t.Fatalf("request path = %q, want collector-scoped events route", gotPath)
	}
}

func TestHTTPBatchSink_PreservesCaseInsensitiveAuthorizationHeader(t *testing.T) {
	var gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      srv.URL + "/events",
		Headers:       map[string]string{"authorization": "Bearer existing"},
		BasicUsername: "dsn-user",
		BasicPassword: "dsn-secret",
		Insecure:      true,
		BatchSize:     1,
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink() error = %v", err)
	}
	defer sink.Close(context.Background())
	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if gotAuthorization != "Bearer existing" {
		t.Fatalf("Authorization = %q, want existing Bearer header", gotAuthorization)
	}
}

func TestCollectorClient_RejectsRemotePlaintextBasicAuth(t *testing.T) {
	client := NewCollectorClient(CollectorClientConfig{
		Endpoint:      "http://collector.example.com",
		BasicUsername: "dsn-user",
		BasicPassword: "dsn-secret",
	})
	_, err := client.Ingest(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "require TLS") {
		t.Fatalf("Ingest() error = %v, want plaintext Basic-auth rejection", err)
	}
}

func TestHTTPBatchSinkRetainsFailedBatchAheadOfNewEvents(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		shouldFail := fail
		mu.Unlock()
		if shouldFail {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	got, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      server.URL,
		Insecure:      true,
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink: %v", err)
	}
	if err := got.WriteEvent(context.Background(), []byte("first\n"), nil); err == nil {
		t.Fatal("first delivery unexpectedly succeeded")
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	if err := got.WriteEvent(context.Background(), []byte("second\n"), nil); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	if err := got.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("request bodies = %q, want two attempts", bodies)
	}
	if bodies[0] != "first\n" || bodies[1] != "first\nsecond\n" {
		t.Fatalf("request bodies = %q, failed batch was not retried first", bodies)
	}
}

func TestHTTPBatchSink_SurfacesCollectorResponseFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{
			name:       "multi-status with invalid ack",
			statusCode: http.StatusMultiStatus,
			body:       `{"status":"partial","accepted":1,"invalid":1,"acks":[{"status":"accepted","reason":"accepted"},{"status":"invalid","reason":"schema_invalid","message":"schema validation failed"}]}`,
			wantErr:    "schema validation failed",
		},
		{
			name:       "ok status with rejected ack",
			statusCode: http.StatusOK,
			body:       `{"status":"rejected","rejected":1,"acks":[{"status":"rejected","reason":"policy","message":"event rejected by collector"}]}`,
			wantErr:    "event rejected by collector",
		},
		{
			name:       "ok status with retryable error",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"code":"rate_limited","message":"slow down","retryable":true}]}`,
			wantErr:    "slow down",
		},
		{
			name:       "accepted response",
			statusCode: http.StatusOK,
			body:       `{"status":"accepted","accepted":1}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
				Endpoint:      srv.URL,
				Insecure:      true,
				BatchSize:     1,
				FlushInterval: time.Hour,
			})
			if err != nil {
				t.Fatalf("HTTPBatchSink() error = %v", err)
			}
			defer sink.Close(context.Background())

			err = sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("WriteEvent() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("WriteEvent() error = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("WriteEvent() error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestHTTPBatchSink_OnErrorReceivesCollectorResponseFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"status":"partial","invalid":1,"acks":[{"status":"invalid","reason":"schema_invalid","message":"schema validation failed"}]}`))
	}))
	defer srv.Close()

	errCh := make(chan error, 1)
	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      srv.URL,
		Insecure:      true,
		BatchSize:     10,
		FlushInterval: 20 * time.Millisecond,
		OnError: func(err error) {
			select {
			case errCh <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink() error = %v", err)
	}
	defer sink.Close(context.Background())

	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}

	select {
	case got := <-errCh:
		if !strings.Contains(got.Error(), "schema validation failed") {
			t.Fatalf("OnError error = %q, want containing %q", got.Error(), "schema validation failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError was not called for the rejected batch")
	}
}
