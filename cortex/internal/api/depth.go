package api

import (
	"context"

	"github.com/astraive/loza/cortex/internal/metrics"
)

// defaultGraphDepth is used when a transport supplies no positive depth.
const defaultGraphDepth = 3

// maxGraphDepth is the single maximum traversal depth enforced by every
// Cortex transport: HTTP, gRPC, GraphQL, WebSocket and GraphQL-over-WebSocket.
const maxGraphDepth = 100

// Transport labels for the shared depth over-limit metric.
const (
	depthTransportHTTP             = "http"
	depthTransportGRPC             = "grpc"
	depthTransportGraphQL          = "graphql"
	depthTransportWebSocket        = "websocket"
	depthTransportGraphQLWebSocket = "graphql-websocket"
)

// clampDepth bounds a graph traversal depth to [defaultGraphDepth,
// maxGraphDepth] and reports whether the supplied value exceeded the maximum.
// Non-positive values fall back to defaultGraphDepth and are not over-limit.
func clampDepth(value int) (int, bool) {
	if value <= 0 {
		return defaultGraphDepth, false
	}
	if value > maxGraphDepth {
		return maxGraphDepth, true
	}
	return value, false
}

// clampedGraphDepth applies clampDepth and records the shared over-limit
// metric for the given transport when the value was above the maximum.
func clampedGraphDepth(transport string, value int) int {
	depth, overLimit := clampDepth(value)
	if overLimit {
		metrics.GraphDepthOverLimit.WithLabelValues(transport).Inc()
	}
	return depth
}

type depthTransportKey struct{}

// withDepthTransport annotates a GraphQL request context with its originating
// transport so the shared clamp helper records the correct metric label.
func withDepthTransport(ctx context.Context, transport string) context.Context {
	return context.WithValue(ctx, depthTransportKey{}, transport)
}

// depthFromContext returns the transport recorded on the context, defaulting
// to plain GraphQL for HTTP-originated queries and direct resolver calls.
func depthFromContext(ctx context.Context) string {
	if transport, ok := ctx.Value(depthTransportKey{}).(string); ok && transport != "" {
		return transport
	}
	return depthTransportGraphQL
}
