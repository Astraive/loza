package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
)

// TestParseSignaturesResponseRejectsMalformedPayloads covers every shape that
// used to panic at signatures.go:38 (missing, null, or wrong-type data and
// signatures fields). The parser must return an error, never panic.
func TestParseSignaturesResponseRejectsMalformedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"empty object", `{}`, "'data'"},
		{"null data", `{"data":null}`, "'data'"},
		{"data wrong type", `{"data":"oops"}`, "'data'"},
		{"data is array", `{"data":[]}`, "'data'"},
		{"missing signatures", `{"data":{}}`, "'signatures'"},
		{"null signatures", `{"data":{"signatures":null}}`, "'signatures'"},
		{"signatures wrong type", `{"data":{"signatures":{}}}`, "'signatures'"},
		{"signatures is string", `{"data":{"signatures":"nope"}}`, "'signatures'"},
		{"invalid json", `not-json`, "decode signatures response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sigs, err := parseSignaturesResponse([]byte(tt.body))
			if err == nil {
				t.Fatalf("expected an error, got signatures %v", sigs)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestParseSignaturesResponseAcceptsValidPayload(t *testing.T) {
	sigs, err := parseSignaturesResponse([]byte(`{"data":{"signatures":[{"id":"s1"}]}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signature, got %d", len(sigs))
	}
}

// TestRunCortexGraphRejectsMalformedResponses covers the unchecked graph["nodes"]
// and graph["edges"] assertions at cortex.go:396-397.
func TestRunCortexGraphRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"missing nodes", `{"edges":[]}`, "'nodes'"},
		{"null nodes", `{"nodes":null,"edges":[]}`, "'nodes'"},
		{"nodes wrong type", `{"nodes":{},"edges":[]}`, "'nodes'"},
		{"missing edges", `{"nodes":[]}`, "'edges'"},
		{"null edges", `{"nodes":[],"edges":null}`, "'edges'"},
		{"edges wrong type", `{"nodes":[],"edges":{}}`, "'edges'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
			err := RunCortexGraph(context.Background(), cfg, []string{"--service", "checkout"})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestOutputIncidentContextRejectsMalformedSuccessRate covers the unchecked
// action["success_rate"].(float64) assertion at cortex.go:229.
func TestOutputIncidentContextRejectsMalformedSuccessRate(t *testing.T) {
	tests := []struct {
		name    string
		ctx     map[string]interface{}
		wantErr string
	}{
		{
			"missing success rate",
			map[string]interface{}{"suggested_actions": []interface{}{map[string]interface{}{"action": "restart"}}},
			"success_rate",
		},
		{
			"null success rate",
			map[string]interface{}{"suggested_actions": []interface{}{map[string]interface{}{"action": "restart", "success_rate": nil}}},
			"success_rate",
		},
		{
			"wrong type success rate",
			map[string]interface{}{"suggested_actions": []interface{}{map[string]interface{}{"action": "restart", "success_rate": "high"}}},
			"success_rate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := captureStdout(t, func() error {
				return outputIncidentContext(tt.ctx)
			})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestOutputIncidentContextAcceptsNumericSuccessRate(t *testing.T) {
	ctx := map[string]interface{}{
		"suggested_actions": []interface{}{
			map[string]interface{}{"action": "restart", "success_rate": 0.5},
		},
	}
	_, err := captureStdout(t, func() error {
		return outputIncidentContext(ctx)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSinksCommandEmptyOrWhitespaceSubcommand covers the args[0][0] panic at
// sinks.go:15. An empty or whitespace-only argument must not crash the CLI.
func TestSinksCommandEmptyOrWhitespaceSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"empty string", []string{""}},
		{"whitespace only", []string{"   "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"sinks":[]}`))
			}))
			defer srv.Close()

			cfg := config.Config{CollectorURL: srv.URL}
			if err := SinksCommand(context.Background(), cfg, tt.args); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
