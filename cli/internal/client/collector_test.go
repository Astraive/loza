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

func readResponseBodyOversizedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", maxCLIResponseBytes+1)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadResponseBodyRejectsOversizedResponse(t *testing.T) {
	_, err := readResponseBody(strings.NewReader(strings.Repeat("x", maxCLIResponseBytes+1)))
	if err == nil {
		t.Fatal("expected an error for a response larger than maxCLIResponseBytes")
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadResponseBodyAllowsResponseAtLimit(t *testing.T) {
	want := strings.Repeat("x", maxCLIResponseBytes)
	got, err := readResponseBody(strings.NewReader(want))
	if err != nil {
		t.Fatalf("unexpected error for a response at the limit: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d bytes, got %d", len(want), len(got))
	}
}

func TestQueryReturnsErrorOnOversizedResponse(t *testing.T) {
	srv := readResponseBodyOversizedServer(t)
	body, err := Query(srv.URL, "duckdb", "select 1")
	if err == nil {
		t.Fatalf("expected an error for an oversized response, got %d bytes", len(body))
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchSchemaReturnsErrorOnOversizedResponse(t *testing.T) {
	srv := readResponseBodyOversizedServer(t)
	body, err := FetchSchema(srv.URL)
	if err == nil {
		t.Fatalf("expected an error for an oversized response, got %d bytes", len(body))
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

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

// TestKeyAndQuarantinePathIDsAreEscaped reproduces MASS-0035: a raw ID such as
// "x/../../keys?force=true" must stay inside one path segment instead of
// injecting extra segments or a query string into the request URL.
func TestKeyAndQuarantinePathIDsAreEscaped(t *testing.T) {
	const maliciousID = "x/../../keys?force=true"

	tests := []struct {
		name     string
		id       string
		wantPath string
		call     func(baseURL, id string) error
	}{
		{
			name:     "revoke key with malicious id",
			id:       maliciousID,
			wantPath: "/keys/x%2F..%2F..%2Fkeys%3Fforce=true/revoke",
			call: func(baseURL, id string) error {
				_, err := RevokeAPIKey(context.Background(), baseURL, id)
				return err
			},
		},
		{
			name:     "rotate key with malicious id",
			id:       maliciousID,
			wantPath: "/keys/x%2F..%2F..%2Fkeys%3Fforce=true/rotate",
			call: func(baseURL, id string) error {
				_, err := RotateAPIKey(context.Background(), baseURL, id)
				return err
			},
		},
		{
			name:     "replay quarantine item with malicious id",
			id:       maliciousID,
			wantPath: "/quarantine/x%2F..%2F..%2Fkeys%3Fforce=true/replay",
			call: func(baseURL, id string) error {
				_, err := ReplayQuarantineItem(context.Background(), baseURL, id)
				return err
			},
		},
		{
			name:     "delete quarantine item with malicious id",
			id:       maliciousID,
			wantPath: "/quarantine/x%2F..%2F..%2Fkeys%3Fforce=true",
			call: func(baseURL, id string) error {
				_, err := DeleteQuarantineItem(context.Background(), baseURL, id)
				return err
			},
		},
		{
			name:     "ordinary id is unchanged",
			id:       "evt_1",
			wantPath: "/keys/evt_1/revoke",
			call: func(baseURL, id string) error {
				_, err := RevokeAPIKey(context.Background(), baseURL, id)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.EscapedPath(); got != tt.wantPath {
					t.Errorf("escaped path = %q, want %q", got, tt.wantPath)
				}
				if got := r.URL.RawQuery; got != "" {
					t.Errorf("unexpected query string %q; id must not inject query parameters", got)
				}
				if got := r.RequestURI; got != tt.wantPath {
					t.Errorf("request URI = %q, want %q", got, tt.wantPath)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			if err := tt.call(srv.URL, tt.id); err != nil {
				t.Fatalf("request failed: %v", err)
			}
		})
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

func TestDLQItemHelpersEscapeIDInPath(t *testing.T) {
	tests := []struct {
		name   string
		method string
		suffix string
		call   func(baseURL, id string) ([]byte, error)
	}{
		{"fetch", http.MethodGet, "", func(baseURL, id string) ([]byte, error) {
			return FetchDLQItem(baseURL, id)
		}},
		{"delete", http.MethodDelete, "", func(baseURL, id string) ([]byte, error) {
			return DeleteDLQItem(context.Background(), baseURL, id)
		}},
		{"replay", http.MethodPost, "/replay", func(baseURL, id string) ([]byte, error) {
			return ReplayDLQItem(context.Background(), baseURL, id)
		}},
	}

	// A raw id containing path traversal, query and fragment delimiters must be
	// escaped into a single path segment instead of altering the request target.
	ids := []string{
		"../../events/by-tenant/x",
		"evil?x=1#frag/../y",
		"dlq_0",
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range ids {
				var gotPath, gotRawQuery string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.EscapedPath()
					gotRawQuery = r.URL.RawQuery
					if r.Method != tc.method {
						t.Errorf("method = %s, want %s", r.Method, tc.method)
					}
					_, _ = w.Write([]byte(`{"ok":true}`))
				}))

				if _, err := tc.call(srv.URL, id); err != nil {
					srv.Close()
					t.Fatalf("call with id %q: %v", id, err)
				}
				srv.Close()

				wantPath := "/dlq/" + url.PathEscape(id) + tc.suffix
				if gotPath != wantPath {
					t.Errorf("id %q: escaped path = %q, want %q", id, gotPath, wantPath)
				}
				if gotRawQuery != "" {
					t.Errorf("id %q: raw query = %q, want empty", id, gotRawQuery)
				}
			}
		})
func TestAPIKeyAuthRequiresTLSForNonLocalHosts(t *testing.T) {
	t.Setenv("LOZA_API_KEY", "lz_sec_live_key")
	t.Setenv("LOZA_COLLECTOR_API_KEY", "")
	t.Setenv("LOZA_CORTEX_API_KEY", "")

	for _, endpoint := range []string{"http://collector.example/status", "http://cortex.example/healthz"} {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyAPIKeyAuth(req); err == nil {
			t.Fatalf("expected %s to reject an API key over plaintext HTTP", endpoint)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("bearer header must not be set on rejected request, got %q", got)
		}
	}

	for _, endpoint := range []string{"https://collector.example/status", "http://127.0.0.1:9308/status", "http://localhost:9308/status"} {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyAPIKeyAuth(req); err != nil {
			t.Fatalf("expected %s to accept the API key, got %v", endpoint, err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer lz_sec_live_key" {
			t.Fatalf("Authorization = %q for %s", got, endpoint)
		}
	}
}
