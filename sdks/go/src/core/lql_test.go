package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryLQLUsesScopedRouteAndScopeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collectors/demo/lql/query" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer api" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Loza-Env") != "prod" || r.Header.Get("X-Loza-Service") != "cli" {
			t.Fatalf("scope headers = %q/%q", r.Header.Get("X-Loza-Env"), r.Header.Get("X-Loza-Service"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"columns": []string{"event_id"}, "rows": []map[string]any{{"event_id": "evt-1"}}, "row_count": 1})
	}))
	defer server.Close()
	client := NewCollectorClient(CollectorClientConfig{Endpoint: server.URL, CollectorName: "demo", APIKey: "api", Environment: "prod", Service: "cli", Insecure: true, Client: server.Client()})
	result, err := client.QueryLQL(context.Background(), "from events | where event_id = $id", LQLQueryOptions{Parameters: map[string]QueryValue{"id": {Type: "string", Value: "evt-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 {
		t.Fatalf("row count = %d", result.RowCount)
	}
}

func TestQueryLQLCompilationErrorPopulatesStatus(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":       "invalid lql",
					"diagnostics": []map[string]any{{"code": "E001", "message": "unknown table"}},
				})
			}))
			defer server.Close()
			client := NewCollectorClient(CollectorClientConfig{Endpoint: server.URL, Insecure: true, Client: server.Client()})

			_, err := client.QueryLQL(context.Background(), "from nope")

			var compileErr *LQLCompilationError
			if !errors.As(err, &compileErr) {
				t.Fatalf("error = %T %v, want *LQLCompilationError", err, err)
			}
			if compileErr.Status != status {
				t.Fatalf("Status = %d, want %d", compileErr.Status, status)
			}
			if compileErr.Message != "invalid lql" {
				t.Fatalf("Message = %q, want %q", compileErr.Message, "invalid lql")
			}
			if len(compileErr.Diagnostics) != 1 || compileErr.Diagnostics[0].Code != "E001" {
				t.Fatalf("Diagnostics = %+v", compileErr.Diagnostics)
			}
		})
	}
}
