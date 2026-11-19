package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	internalserver "github.com/astraive/loza/collector/internal/server"
)

func New(cfg internalserver.HTTPConfig, state internalserver.State) *internalserver.HTTPServer {
	return internalserver.NewHTTPServer(cfg, state)
}

type PublicHandlerSet interface {
	HandleIngest(http.ResponseWriter, *http.Request)
	HandleOTLPLogs(http.ResponseWriter, *http.Request)
	HandleHealth(http.ResponseWriter, *http.Request)
	HandleReady(http.ResponseWriter, *http.Request)
	HandleVersion(http.ResponseWriter, *http.Request)
	HandleStatus(http.ResponseWriter, *http.Request)
	HandleValidate(http.ResponseWriter, *http.Request)
	HandleSinks(http.ResponseWriter, *http.Request)
	HandleSink(http.ResponseWriter, *http.Request)
	HandleSinkTest(http.ResponseWriter, *http.Request)
	HandleSchemaList(http.ResponseWriter, *http.Request)
	HandleSchemaDiff(http.ResponseWriter, *http.Request)
	HandleSchemaCheck(http.ResponseWriter, *http.Request)
	HandleSchemaPublish(http.ResponseWriter, *http.Request)
	HandleBlueprintPublish(http.ResponseWriter, *http.Request)
	HandleBlueprintList(http.ResponseWriter, *http.Request)
	HandleQuery(http.ResponseWriter, *http.Request)
	HandleLQLQuery(http.ResponseWriter, *http.Request)
	HandlePIIAudit(http.ResponseWriter, *http.Request)
	HandlePolicyValidate(http.ResponseWriter, *http.Request)
	HandleRetentionApply(http.ResponseWriter, *http.Request)
	HandleKeyCreate(http.ResponseWriter, *http.Request)
	HandleKeyRevoke(http.ResponseWriter, *http.Request)
	HandleKeyRotate(http.ResponseWriter, *http.Request)
	HandleDeleteEvents(http.ResponseWriter, *http.Request)
	HandleDLQList(http.ResponseWriter, *http.Request)
	HandleDLQReplayAll(http.ResponseWriter, *http.Request)
	HandleDLQShow(http.ResponseWriter, *http.Request)
	HandleDLQReplay(http.ResponseWriter, *http.Request)
	HandleDLQDelete(http.ResponseWriter, *http.Request)
	HandleTail(http.ResponseWriter, *http.Request)
	HandleReplay(http.ResponseWriter, *http.Request)
	HandleDatabaseConnections(http.ResponseWriter, *http.Request)
	HandleDatabaseConnectionTest(http.ResponseWriter, *http.Request)
	HandleDatabaseQuery(http.ResponseWriter, *http.Request)
}

// RouteProtector wraps an http.Handler with a permission check.
// The perm string is the required permission name (e.g., "events:write").
type RouteProtector func(next http.Handler, perm string) http.Handler

// CollectorResolver resolves the target collector for a data-plane request.
// It returns false when the request has no authorized collector mapping.
type CollectorResolver func(*http.Request) (string, bool)

// CollectorRouteMode identifies whether a data route uses canonical resource
// authorization or its explicitly configured legacy default mapping.
type CollectorRouteMode uint8

const (
	CanonicalCollectorRoute CollectorRouteMode = iota
	LegacyCollectorRoute
)

// CollectorRouteProtector authorizes a route after resolving its collector.
// Implementations must attach the resolved collector and environment only after
// authorization succeeds. Canonical routes require collector grants; legacy
// routes retain the configured default collector's compatibility policy.
type CollectorRouteProtector func(next http.Handler, perm string, resolve CollectorResolver, mode CollectorRouteMode) http.Handler

// AuthorizedCollector is the server-authorized resource scope for a request.
// It is never populated from event or query payload fields.
type AuthorizedCollector struct {
	Name        string
	Environment string
}

type authorizedCollectorContextKey struct{}

// WithAuthorizedCollector attaches an already-authorized collector scope.
func WithAuthorizedCollector(ctx context.Context, collector, environment string) context.Context {
	return context.WithValue(ctx, authorizedCollectorContextKey{}, AuthorizedCollector{
		Name:        collector,
		Environment: environment,
	})
}

// AuthorizedCollectorFromContext returns the collector scope authorized for the
// current request. It is absent before route authorization succeeds.
func AuthorizedCollectorFromContext(ctx context.Context) (AuthorizedCollector, bool) {
	scope, ok := ctx.Value(authorizedCollectorContextKey{}).(AuthorizedCollector)
	return scope, ok
}

// BuildMux constructs the HTTP route table. Collector data routes are
// registered under /collectors/{collector}/... and passed through
// collectorProtect. Legacy routes are registered only when defaultCollector is
// explicitly configured, and are bound to that collector by the resolver.
// Public routes (health, ready, version) are never wrapped.
func BuildMux(ingestPath, healthPath, readyPath, metricsPath string, metricsEnabled bool, metricsHandler http.Handler, tailWebSocketHandler http.Handler, handlers PublicHandlerSet, protect RouteProtector, collectorProtect CollectorRouteProtector, defaultCollector string) *http.ServeMux {
	mux := http.NewServeMux()
	defaultCollector = strings.TrimSpace(defaultCollector)

	// registeredPatterns records every pattern registered on the mux. Go 1.22's
	// ServeMux panics when the same method+pattern is registered twice, so all
	// registrations go through the reserve/register helpers below. Configurable
	// paths are registered last and skipped when they would collide with a
	// built-in route.
	registeredPatterns := make(map[string]struct{})
	reserve := func(pattern string) bool {
		if _, exists := registeredPatterns[pattern]; exists {
			return false
		}
		registeredPatterns[pattern] = struct{}{}
		return true
	}
	register := func(pattern string, h http.Handler) {
		if reserve(pattern) {
			mux.Handle(pattern, h)
		}
	}
	registerFunc := func(pattern string, fn http.HandlerFunc) {
		if reserve(pattern) {
			mux.HandleFunc(pattern, fn)
		}
	}

	scopedOperationUnsupported := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "scoped_operation_unsupported"})
	}

	// registerDataRoute registers the canonical collector-scoped endpoint and,
	// only during an explicit migration, its legacy default-collector equivalent.
	registerDataRoute := func(method, pattern string, canonical, legacy http.Handler, routePerm, collectorPerm string) {
		wrap := func(h http.Handler, resolve CollectorResolver, mode CollectorRouteMode) http.Handler {
			next := h
			if collectorProtect != nil {
				next = collectorProtect(next, collectorPerm, resolve, mode)
			}
			// The historical permission vocabulary remains only on explicitly
			// configured legacy routes. Canonical routes use collector grants.
			if mode == LegacyCollectorRoute && protect != nil && routePerm != "" {
				next = protect(next, routePerm)
			}
			return next
		}

		register(method+" /collectors/{collector}"+pattern, wrap(canonical, func(r *http.Request) (string, bool) {
			collector := strings.TrimSpace(r.PathValue("collector"))
			return collector, collector != ""
		}, CanonicalCollectorRoute))
		if defaultCollector != "" {
			register(method+" "+pattern, wrap(legacy, func(*http.Request) (string, bool) {
				return defaultCollector, true
			}, LegacyCollectorRoute))
		}
	}
	dataRoute := func(method, pattern string, h http.Handler, routePerm, collectorPerm string) {
		registerDataRoute(method, pattern, h, h, routePerm, collectorPerm)
	}

	// ── Ingest (events:write) ────────────────────────────────────────────
	// The configurable ingest path is registered at the end of BuildMux, once
	// every built-in data route is known, so it can be skipped when it would
	// collide with one of them (see below).
	dataRoute("POST", "/events", http.HandlerFunc(handlers.HandleIngest), "events:write", "events:write")
	dataRoute("POST", "/ingest", http.HandlerFunc(handlers.HandleIngest), "events:write", "events:write")
	dataRoute("POST", "/events/batch", http.HandlerFunc(handlers.HandleIngest), "events:write", "events:write")
	dataRoute("POST", "/events/ndjson", http.HandlerFunc(handlers.HandleIngest), "events:write", "events:write")
	dataRoute("POST", "/validate", http.HandlerFunc(handlers.HandleValidate), "schema:read", "events:read")
	registerDataRoute("POST", "/otlp/logs", http.HandlerFunc(scopedOperationUnsupported), http.HandlerFunc(handlers.HandleOTLPLogs), "logs:write", "logs:write")

	// ── Public (no auth) ─────────────────────────────────────────────────
	// Custom health/ready paths are registered at the end of BuildMux so
	// built-in routes always win a collision instead of causing a panic.
	registerFunc("GET /health", handlers.HandleHealth)
	registerFunc("GET /healthz", handlers.HandleHealth)
	registerFunc("GET /ready", handlers.HandleReady)
	registerFunc("GET /readyz", handlers.HandleReady)
	registerFunc("GET /version", handlers.HandleVersion)
	dataRoute("GET", "/status", http.HandlerFunc(handlers.HandleStatus), "status:read", "events:read")

	// ── Read endpoints ───────────────────────────────────────────────────
	dataRoute("GET", "/sinks", http.HandlerFunc(handlers.HandleSinks), "events:read", "events:read")
	dataRoute("GET", "/sinks/{name}", http.HandlerFunc(handlers.HandleSink), "events:read", "events:read")
	dataRoute("POST", "/sinks/{name}/test", http.HandlerFunc(handlers.HandleSinkTest), "events:write", "events:write")
	dataRoute("GET", "/schema", http.HandlerFunc(handlers.HandleSchemaList), "schema:read", "events:read")
	dataRoute("POST", "/schema/check", http.HandlerFunc(handlers.HandleSchemaCheck), "schema:read", "events:read")
	dataRoute("POST", "/schema/diff", http.HandlerFunc(handlers.HandleSchemaDiff), "schema:read", "events:read")
	dataRoute("POST", "/query", http.HandlerFunc(handlers.HandleQuery), "events:read", "events:read")
	registerDataRoute(
		"POST",
		"/lql/query",
		http.HandlerFunc(handlers.HandleLQLQuery),
		http.HandlerFunc(handlers.HandleLQLQuery),
		"events:read",
		"events:read",
	)
	dataRoute("GET", "/database/connections", http.HandlerFunc(handlers.HandleDatabaseConnections), "events:read", "events:read")
	dataRoute("POST", "/database/connections/{name}/test", http.HandlerFunc(handlers.HandleDatabaseConnectionTest), "events:read", "events:read")
	dataRoute("POST", "/database/query", http.HandlerFunc(handlers.HandleDatabaseQuery), "events:read", "events:read")
	dataRoute("GET", "/schema/blueprint", http.HandlerFunc(handlers.HandleBlueprintList), "schema:read", "events:read")

	// ── Tail (events:read) ───────────────────────────────────────────────
	dataRoute("GET", "/tail", http.HandlerFunc(handlers.HandleTail), "events:read", "events:read")

	// ── Write endpoints ──────────────────────────────────────────────────
	dataRoute("POST", "/schema/publish", http.HandlerFunc(handlers.HandleSchemaPublish), "schema:write", "project:admin")
	dataRoute("POST", "/schema/blueprint", http.HandlerFunc(handlers.HandleBlueprintPublish), "schema:write", "project:admin")

	// ── Replay ───────────────────────────────────────────────────────────
	dataRoute("POST", "/replay", http.HandlerFunc(handlers.HandleReplay), "events:write", "events:write")

	// ── Admin endpoints ──────────────────────────────────────────────────
	registerDataRoute("POST", "/audit/pii", http.HandlerFunc(scopedOperationUnsupported), http.HandlerFunc(handlers.HandlePIIAudit), "pii_audit:read", "project:admin")
	dataRoute("POST", "/policy/validate", http.HandlerFunc(handlers.HandlePolicyValidate), "schema:read", "project:admin")
	registerDataRoute("POST", "/retention/apply", http.HandlerFunc(scopedOperationUnsupported), http.HandlerFunc(handlers.HandleRetentionApply), "project:admin", "project:admin")
	dataRoute("POST", "/keys", http.HandlerFunc(handlers.HandleKeyCreate), "project:admin", "project:admin")
	dataRoute("POST", "/keys/{id}/revoke", http.HandlerFunc(handlers.HandleKeyRevoke), "project:admin", "project:admin")
	dataRoute("DELETE", "/keys/{id}", http.HandlerFunc(handlers.HandleKeyRevoke), "project:admin", "project:admin")
	dataRoute("POST", "/keys/{id}/rotate", http.HandlerFunc(handlers.HandleKeyRotate), "project:admin", "project:admin")
	dataRoute("DELETE", "/events", http.HandlerFunc(handlers.HandleDeleteEvents), "events:delete", "events:delete")
	dataRoute("DELETE", "/events/by-tenant/{tenant_id}", http.HandlerFunc(handlers.HandleDeleteEvents), "events:delete", "events:delete")
	dataRoute("DELETE", "/events/by-user/{user_id}", http.HandlerFunc(handlers.HandleDeleteEvents), "events:delete", "events:delete")
	dataRoute("DELETE", "/events/{event_id}", http.HandlerFunc(handlers.HandleDeleteEvents), "events:delete", "events:delete")

	// ── DLQ ──────────────────────────────────────────────────────────────
	dataRoute("GET", "/dlq", http.HandlerFunc(handlers.HandleDLQList), "events:read", "events:read")
	dataRoute("POST", "/dlq/replay", http.HandlerFunc(handlers.HandleDLQReplayAll), "events:write", "events:write")
	dataRoute("GET", "/dlq/{id}", http.HandlerFunc(handlers.HandleDLQShow), "events:read", "events:read")
	dataRoute("POST", "/dlq/{id}/replay", http.HandlerFunc(handlers.HandleDLQReplay), "events:write", "events:write")
	dataRoute("DELETE", "/dlq/{id}", http.HandlerFunc(handlers.HandleDLQDelete), "events:delete", "events:delete")

	// ── WebSocket tail ───────────────────────────────────────────────────
	if tailWebSocketHandler != nil {
		registerDataRoute(
			"GET",
			"/ws/tail",
			tailWebSocketHandler,
			tailWebSocketHandler,
			"events:read",
			"events:read",
		)
	}

	// ── Metrics (protected by default) ───────────────────────────────────
	if metricsEnabled && metricsHandler != nil && metricsPath != "" {
		if protect != nil {
			register("GET "+metricsPath, protect(metricsHandler, "events:read"))
		} else {
			register("GET "+metricsPath, metricsHandler)
		}
	}

	// ── Custom configurable paths ────────────────────────────────────────
	// Register these last so every built-in route is already reserved. The
	// register/registerFunc helpers silently skip a pattern that is already
	// taken, so a config value such as routes.ingest=/validate can no longer
	// trigger a duplicate-registration panic at startup. Previously the guard
	// excluded only /events and /ingest for ingest, and a couple of built-ins
	// for health and ready, leaving all other collisions unguarded.
	if ingestPath != "" {
		dataRoute("POST", ingestPath, http.HandlerFunc(handlers.HandleIngest), "events:write", "events:write")
	}
	if healthPath != "" {
		registerFunc("GET "+healthPath, handlers.HandleHealth)
	}
	if readyPath != "" {
		registerFunc("GET "+readyPath, handlers.HandleReady)
	}

	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOZA_ENABLE_PPROF"))); v == "1" || v == "true" {
		registerPprof(mux)
	}
	return mux
}
