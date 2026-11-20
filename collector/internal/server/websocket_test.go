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
