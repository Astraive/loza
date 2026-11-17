package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resetAllowedOrigins(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetAllowedOrigins() })
}

func TestAPIKeyOnlyAttachedToAllowedHost(t *testing.T) {
	t.Setenv("LOZA_API_KEY", "super-secret-key")
	resetAllowedOrigins(t)

	var allowedAuth, blockedAuth atomic.Value
	allowedAuth.Store("")
	blockedAuth.Store("")

	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer allowed.Close()
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blockedAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer blocked.Close()

	SetAllowedOrigins(allowed.URL)

	if _, err := FetchStatus(allowed.URL); err != nil {
		t.Fatalf("fetch from allowed host: %v", err)
	}
	if _, err := FetchStatus(blocked.URL); err != nil {
		t.Fatalf("fetch from blocked host: %v", err)
	}
	if got := allowedAuth.Load().(string); got != "Bearer super-secret-key" {
		t.Fatalf("expected key on allowed host, got %q", got)
	}
	if got := blockedAuth.Load().(string); got != "" {
		t.Fatalf("credential leaked to unconfigured host: %q", got)
	}
}

func TestNoAPIKeyAttachedWhenNoOriginsConfigured(t *testing.T) {
	t.Setenv("LOZA_API_KEY", "super-secret-key")
	SetAllowedOrigins()
	resetAllowedOrigins(t)

	var gotAuth atomic.Value
	gotAuth.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := FetchStatus(srv.URL); err != nil {
		t.Fatalf("fetch status: %v", err)
	}
	if got := gotAuth.Load().(string); got != "" {
		t.Fatalf("expected no Authorization header with empty allowlist, got %q", got)
	}
}

func TestIsAllowedURLRejectsSSRFStyleInputs(t *testing.T) {
	SetAllowedOrigins("http://trusted.example:9308")
	resetAllowedOrigins(t)

	cases := []struct {
		raw  string
		want bool
	}{
		{"http://trusted.example:9308/events", true},
		{"http://trusted.example:9308", true},
		{"http://trusted.example/events", false},              // different (default) port
		{"https://trusted.example:9308/events", false},        // scheme mismatch
		{"http://trusted.example:9309/events", false},         // port mismatch
		{"http://TRUSTED.EXAMPLE:9308/events", true},          // host case-insensitive
		{"http://evil.example/events", false},                 // unconfigured host
		{"http://169.254.169.254/latest/meta-data/", false},   // cloud metadata
		{"http://127.0.0.1:9308/events", false},               // look-alike loopback
		{"file:///etc/passwd", false},                         // non-http scheme
		{"gopher://trusted.example:9308/x", false},            // disallowed scheme
		{"//evil.example/events", false},                      // scheme-relative
		{"http://user@evil.example/events", false},            // userinfo trickery
		{"http://trusted.example:9308@evil.example/x", false}, // userinfo host confusion
		{"not a url", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsAllowedURL(tc.raw); got != tc.want {
			t.Errorf("IsAllowedURL(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestFetchEventsFromURLRefusesUnconfiguredHost(t *testing.T) {
	resetAllowedOrigins(t)
	SetAllowedOrigins("http://127.0.0.1:1")

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := FetchEventsFromURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for unconfigured fetch host")
	} else if !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server should not be contacted, got %d hits", hits.Load())
	}
}

func TestFetchEventsFromURLChecksStatus(t *testing.T) {
	resetAllowedOrigins(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	SetAllowedOrigins(srv.URL)

	if _, err := FetchEventsFromURL(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for non-2xx response")
	} else if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected status in error, got %v", err)
	}
}

func TestFetchEventsFromURLReturnsEvents(t *testing.T) {
	resetAllowedOrigins(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"event":"payment.completed","service":"checkout"}]`))
	}))
	defer srv.Close()
	SetAllowedOrigins(srv.URL)

	events, err := FetchEventsFromURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch events: %v", err)
	}
	if len(events) != 1 || events[0]["event"] != "payment.completed" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestFetchEventsFromURLHonorsContextCancellation(t *testing.T) {
	resetAllowedOrigins(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	SetAllowedOrigins(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := FetchEventsFromURL(ctx, srv.URL); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestOutboundRequestHonorsContextCancellation(t *testing.T) {
	resetAllowedOrigins(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	SetAllowedOrigins(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := IngestCortexBatch(ctx, srv.URL, []map[string]any{{"id": "1"}}); err == nil {
		t.Fatal("expected context cancellation error for ingest request")
	}
}

func TestClientRequestsHaveTimeout(t *testing.T) {
	if got := clientWithTimeout(defaultTimeout).Timeout; got <= 0 {
		t.Fatalf("expected positive client timeout, got %v", got)
	}
}
