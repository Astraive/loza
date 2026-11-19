package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/astraive/loza/cortex/internal/eventconv"
	"github.com/astraive/loza/cortex/internal/middleware"
	transportcontracts "github.com/astraive/loza/spec/transport/contracts"
	"github.com/gorilla/websocket"
)

// newCortexWSUpgrader creates a websocket upgrader with origin allowlist.
func newCortexWSUpgrader(allowedOrigins []string) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  16 * 1024,
		WriteBufferSize: 16 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // Non-browser clients (curl, SDKs)
			}
			// Check configured allowlist
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					return true
				}
			}
			// Allow loopback origins for development. Compare the parsed scheme and
			// host exactly (with an optional numeric port) rather than matching a
			// string prefix: values like "http://localhost:80@evil.com" put the
			// real host in userinfo and must not be treated as localhost.
			if isDevelopmentLoopbackOrigin(origin) {
				return true
			}
			return false
		},
	}
}

// isDevelopmentLoopbackOrigin reports whether origin is a plain http Origin for
// a loopback host, optionally followed by a numeric port. The origin is parsed
// instead of prefix-matched so that crafted values such as
// "http://localhost:80@evil.com" (userinfo) or "http://localhost:evil.com"
// (non-numeric port) cannot bypass the allowlist.
func isDevelopmentLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Scheme, "http") || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	host := strings.ToLower(u.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}

	if port := u.Port(); port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return false
		}
	}
	return true
}

func (s *Server) WebSocketHandler() http.Handler {
	upgrader := newCortexWSUpgrader(s.config.Server.AllowedOrigins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Limit maximum frame size to prevent memory exhaustion
		conn.SetReadLimit(1 * 1024 * 1024) // 1MB max frame

		for {
			var req transportcontracts.WebSocketRequest
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			resp, err := s.executeWebSocketAction(r.Context(), req)
			if err != nil {
				_ = conn.WriteJSON(transportcontracts.WebSocketResponse{Type: "error", Error: err.Error()})
				continue
			}
			_ = conn.WriteJSON(transportcontracts.WebSocketResponse{Type: "result", Data: resp})
		}
	})
}

func (s *Server) executeWebSocketAction(ctx context.Context, req transportcontracts.WebSocketRequest) (any, error) {
	// Write operations require writer role (defense-in-depth; HTTP middleware is primary gate).
	// Batch ingestion is served by the HTTP endpoint, not by this WebSocket dispatcher.
	if req.Action == "ingest_event" {
		if !hasWriterRole(ctx) {
			return nil, fmt.Errorf("writer role required for ingest operations")
		}
	}

	switch req.Action {
	case "healthz":
		return map[string]any{"status": "OK", "ready": s.ready}, nil
	case "ingest_event":
		event, err := eventconv.FromRawMap(req.Event, "loza")
		if err != nil {
			return nil, err
		}
		if err := s.processor.ProcessEvent(ctx, event); err != nil {
			return nil, err
		}
		return map[string]any{"status": "accepted", "id": event.ID}, nil
	case "reconstruct":
		if req.Mode == "deep" {
			return s.recon.ReconstructDeep(ctx, req.IncidentID)
		}
		return s.recon.ReconstructFast(ctx, req.IncidentID)
	case "service_graph":
		depth := req.Depth
		if depth <= 0 {
			depth = 3
		}
		if depth > 100 {
			depth = 100
		}
		return s.graph.GetServiceGraph(ctx, req.Service, depth)
	case "incident_graph":
		depth := req.Depth
		if depth <= 0 {
			depth = 3
		}
		if depth > 100 {
			depth = 100
		}
		return s.graph.GetIncidentGraph(ctx, req.IncidentID, depth)
	case "graphql":
		query := req.Query
		vars := req.Variables
		if query == "" && vars != nil {
			if candidate, ok := vars["query"].(string); ok {
				query = candidate
			}
		}
		return s.graphql.executeQuery(ctx, query, vars, req.OperationName), nil
	default:
		return nil, fmt.Errorf("unknown websocket action %q", req.Action)
	}
}

// hasWriterRole checks the auth context for writer-level permission.
func hasWriterRole(ctx context.Context) bool {
	result := middleware.GetAuthResult(ctx)
	if result == nil {
		// No auth context means auth is disabled; allow (matches HTTP middleware behavior)
		return true
	}
	roleHierarchy := map[string]int{
		"reader": 1,
		"writer": 2,
		"admin":  3,
	}
	currentLevel := roleHierarchy[result.Role]
	return currentLevel >= roleHierarchy["writer"]
}
