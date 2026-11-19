package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/cortex/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestClampDepth(t *testing.T) {
	tests := []struct {
		name      string
		value     int
		want      int
		overLimit bool
	}{
		{name: "zero uses default", value: 0, want: defaultGraphDepth},
		{name: "negative uses default", value: -5, want: defaultGraphDepth},
		{name: "lower bound", value: 1, want: 1},
		{name: "within range", value: 50, want: 50},
		{name: "at maximum", value: maxGraphDepth, want: maxGraphDepth},
		{name: "one over maximum clamps", value: maxGraphDepth + 1, want: maxGraphDepth, overLimit: true},
		{name: "far over maximum clamps", value: 1_000_000, want: maxGraphDepth, overLimit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, overLimit := clampDepth(test.value)
			if got != test.want || overLimit != test.overLimit {
				t.Fatalf("clampDepth(%d) = (%d, %t), want (%d, %t)", test.value, got, overLimit, test.want, test.overLimit)
			}
		})
	}
}

func TestClampedGraphDepthRecordsOverLimitPerTransport(t *testing.T) {
	transports := []string{
		depthTransportGRPC,
		depthTransportGraphQL,
		depthTransportWebSocket,
		depthTransportGraphQLWebSocket,
	}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			counter := metrics.GraphDepthOverLimit.WithLabelValues(transport)
			before := testutil.ToFloat64(counter)
			if got := clampedGraphDepth(transport, maxGraphDepth+1); got != maxGraphDepth {
				t.Fatalf("clampedGraphDepth(%s, %d) = %d, want %d", transport, maxGraphDepth+1, got, maxGraphDepth)
			}
			if after := testutil.ToFloat64(counter); after != before+1 {
				t.Fatalf("over-limit metric for %s went from %v to %v, want +1", transport, before, after)
			}
		})
	}
}

func TestHTTPGraphDepthOverLimitRejectsAndRecordsMetric(t *testing.T) {
	counter := metrics.GraphDepthOverLimit.WithLabelValues(depthTransportHTTP)
	before := testutil.ToFloat64(counter)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/graphs/service?depth=%d", maxGraphDepth+1), nil)
	if _, err := graphDepth(req); err == nil {
		t.Fatal("expected HTTP over-limit depth to be rejected")
	}
	if after := testutil.ToFloat64(counter); after != before+1 {
		t.Fatalf("HTTP over-limit metric went from %v to %v, want +1", before, after)
	}
}

func TestGraphQLQueryDepthValidationSharedByWebSocket(t *testing.T) {
	deepQuery := strings.Repeat("{", maxGraphQLDepth+1) + strings.Repeat("}", maxGraphQLDepth+1)
	if err := validateGraphQLQueryDepth(deepQuery); err == nil {
		t.Fatal("expected deeply nested GraphQL query to be rejected")
	}
	if err := validateGraphQLQueryDepth("query { health }"); err != nil {
		t.Fatalf("shallow query rejected: %v", err)
	}

	// executeQuery is the single entry point used by GraphQL-over-WebSocket;
	// it must apply the same check so the WS path cannot bypass it.
	s := &GraphQLServer{}
	result := s.executeQuery(context.Background(), deepQuery, nil, "")
	if len(result.Errors) == 0 {
		t.Fatal("executeQuery accepted a deeply nested query (WS bypass)")
	}

	ctx := withDepthTransport(context.Background(), depthTransportGraphQLWebSocket)
	if got := depthFromContext(ctx); got != depthTransportGraphQLWebSocket {
		t.Fatalf("depth transport from context = %q, want %q", got, depthTransportGraphQLWebSocket)
	}
	if got := depthFromContext(context.Background()); got != depthTransportGraphQL {
		t.Fatalf("default depth transport = %q, want %q", got, depthTransportGraphQL)
	}
}
