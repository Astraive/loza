package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
)

func TestDeleteCommandValidIdentifiersHitExpectedPath(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"deleted":1}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	cases := []struct {
		target string
		id     string
		want   string
	}{
		{"tenant", "acme-corp", "/events/by-tenant/acme-corp"},
		{"user", "user-12345", "/events/by-user/user-12345"},
		{"event", "evt:abc-123", "/events/evt:abc-123"},
	}
	for _, tc := range cases {
		mu.Lock()
		paths = nil
		mu.Unlock()

		if err := DeleteCommand(context.Background(), cfg, []string{tc.target, tc.id}); err != nil {
			t.Fatalf("delete %s %q: %v", tc.target, tc.id, err)
		}
		mu.Lock()
		if len(paths) != 1 || paths[0] != tc.want {
			t.Fatalf("delete %s %q: got paths %v, want [%s]", tc.target, tc.id, paths, tc.want)
		}
		mu.Unlock()
	}
}

func TestDeleteCommandRejectsUnsafeIdentifiers(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"deleted":1}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	unsafe := []string{
		"tenant?all=true", // injects a query string
		"user#fragment",   // injects a URL fragment
		"../events",       // path traversal
		"a/b",             // injects an extra path segment
		"tenant/../user",  // path traversal across segments
		"with space",      // unsafe path character
		"tenant%2Ffoo",    // percent-encoding to smuggle a separator
		"",                // empty identifier
	}
	for _, id := range unsafe {
		for _, target := range []string{"tenant", "user", "event"} {
			if err := DeleteCommand(context.Background(), cfg, []string{target, id}); err == nil {
				t.Errorf("delete %s %q: expected error for unsafe identifier", target, id)
			}
		}
	}

	if requests != 0 {
		t.Fatalf("expected no HTTP requests for unsafe identifiers, got %d", requests)
	}
}
