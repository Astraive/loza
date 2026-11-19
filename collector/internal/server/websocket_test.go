package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/auth"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebSocketUpgraderOriginPolicy pins the origin-allowlist behavior of the
// upgrader returned by NewWebSocketUpgrader. A former package-level upgrader
// duplicated this policy but ignored the configured allowlist; the handler must
// rely on NewWebSocketUpgrader so configured non-localhost origins are honored.
func TestWebSocketUpgraderOriginPolicy(t *testing.T) {
	upgrader := NewWebSocketUpgrader([]string{"https://console.example.test"})

	tests := []struct {
		name       string
		origin     string
		wantAccept bool
	}{
		{name: "configured allowlist origin accepted", origin: "https://console.example.test", wantAccept: true},
		{name: "unconfigured external origin rejected", origin: "https://evil.example.com", wantAccept: false},
		{name: "no origin (non-browser) accepted", origin: "", wantAccept: true},
		{name: "localhost dev origin accepted", origin: "http://localhost:3000", wantAccept: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			assert.Equal(t, tc.wantAccept, upgrader.CheckOrigin(req))
		})
	}
}

// TestTailWebSocketHandlerOriginAllowlist verifies the handler uses the
// allowlist-configured upgrader (the single source of truth) rather than a dead
// upgrader with a weaker origin policy.
func TestTailWebSocketHandlerOriginAllowlist(t *testing.T) {
	const allowedOrigin = "https://console.example.test"

	newHandler := func() (http.Handler, *websocketTailState) {
		state := &websocketTailState{
			filters:    make(chan TailFilters, 1),
			subscriber: make(chan chan []byte, 1),
			removed:    make(chan struct{}),
		}
		return NewTailWebSocketHandler(HTTPConfig{AllowedOrigins: []string{allowedOrigin}}, state), state
	}
	dialURL := func(srv *httptest.Server) string {
		return "ws" + strings.TrimPrefix(srv.URL, "http")
	}

	t.Run("configured origin accepted", func(t *testing.T) {
		handler, state := newHandler()
		srv := httptest.NewServer(handler)
		defer srv.Close()

		header := http.Header{}
		header.Set("Origin", allowedOrigin)
		conn, _, err := (&websocket.Dialer{Subprotocols: []string{auth.WebSocketTailProtocol}}).
			Dial(dialURL(srv), header)
		require.NoError(t, err)
		_ = conn.Close()

		select {
		case <-state.removed:
		case <-time.After(2 * time.Second):
			t.Fatal("tail subscriber was not removed after disconnect")
		}
	})

	t.Run("unconfigured origin rejected", func(t *testing.T) {
		handler, _ := newHandler()
		srv := httptest.NewServer(handler)
		defer srv.Close()

		header := http.Header{}
		header.Set("Origin", "https://evil.example.com")
		_, resp, err := (&websocket.Dialer{Subprotocols: []string{auth.WebSocketTailProtocol}}).
			Dial(dialURL(srv), header)
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}
