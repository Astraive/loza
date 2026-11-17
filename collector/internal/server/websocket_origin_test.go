package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketUpgraderOriginPolicy(t *testing.T) {
	const consoleOrigin = "https://console.example.test"

	tests := []struct {
		name           string
		allowedOrigins []string
		allowLocalhost bool
		origin         string
		want           bool
	}{
		// Production mode: loopback and Origin-less requests are rejected.
		{"prod/empty origin rejected", nil, false, "", false},
		{"prod/localhost rejected", nil, false, "http://localhost:3000", false},
		{"prod/127.0.0.1 rejected", nil, false, "http://127.0.0.1:3000", false},
		{"prod/ipv6 loopback rejected", nil, false, "http://[::1]:3000", false},
		{"prod/unlisted origin rejected", nil, false, "https://evil.example", false},

		// Production mode: only explicitly configured origins are accepted.
		{"prod/configured origin allowed", []string{consoleOrigin}, false, consoleOrigin, true},
		{"prod/unlisted origin rejected with allowlist", []string{consoleOrigin}, false, "https://other.example", false},
		{"prod/localhost rejected with unrelated allowlist", []string{consoleOrigin}, false, "http://localhost:3000", false},
		{"prod/localhost allowed when explicitly configured", []string{"http://localhost:3000"}, false, "http://localhost:3000", true},
		{"prod/localhost lookalike rejected", []string{"http://localhost:3000"}, false, "http://localhost:3000.evil.example", false},

		// Development mode: Origin-less and loopback requests are allowed, but
		// unlisted remote origins still are not.
		{"dev/empty origin allowed", nil, true, "", true},
		{"dev/localhost allowed", nil, true, "http://localhost", true},
		{"dev/localhost with port allowed", nil, true, "http://localhost:5173", true},
		{"dev/127.0.0.1 allowed", nil, true, "http://127.0.0.1:8080", true},
		{"dev/ipv6 loopback allowed", nil, true, "http://[::1]:8080", true},
		{"dev/unlisted origin rejected", nil, true, "https://evil.example", false},
		{"dev/localhost lookalike rejected", nil, true, "http://localhost.evil.example:3000", false},
		{"dev/https localhost rejected", nil, true, "https://localhost:3000", false},
		{"dev/configured origin allowed", []string{consoleOrigin}, true, consoleOrigin, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgrader := NewWebSocketUpgrader(tt.allowedOrigins, tt.allowLocalhost)
			if upgrader.CheckOrigin == nil {
				t.Fatal("CheckOrigin is nil")
			}
			req := httptest.NewRequest(http.MethodGet, "http://collector.example/tail", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := upgrader.CheckOrigin(req); got != tt.want {
				t.Fatalf("CheckOrigin(origin=%q, allowlist=%v, allowLocalhost=%v) = %v, want %v",
					tt.origin, tt.allowedOrigins, tt.allowLocalhost, got, tt.want)
			}
		})
	}
}

func TestTailWebSocketHandlerRejectsDisallowedOriginsInProduction(t *testing.T) {
	state := &websocketTailState{
		filters:    make(chan TailFilters, 1),
		subscriber: make(chan chan []byte, 1),
		removed:    make(chan struct{}),
	}
	handler := NewTailWebSocketHandler(HTTPConfig{
		AllowedOrigins: []string{"https://console.example.test"},
	}, state)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, origin := range []string{"", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000", "https://evil.example"} {
		t.Run("origin="+origin, func(t *testing.T) {
			header := http.Header{}
			if origin != "" {
				header.Set("Origin", origin)
			}
			conn, resp, err := (&websocket.Dialer{}).Dial(wsURL, header)
			if err == nil {
				_ = conn.Close()
				t.Fatalf("dial with origin %q succeeded, want rejection", origin)
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("dial with origin %q returned status %v, want 403", origin, resp)
			}
		})
	}
}
