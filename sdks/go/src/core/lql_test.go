package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// lqlRequestSnapshot records the parts of an incoming LQL request that the
// tests care about. Handlers capture the request and assert on it from the
// test goroutine: calling t.Fatalf/FailNow from a handler goroutine would run
// runtime.Goexit there, aborting before a response is written.
type lqlRequestSnapshot struct {
	Path          string
	Authorization string
	Env           string
	Service       string
}

func snapshotLQLRequest(r *http.Request) lqlRequestSnapshot {
	return lqlRequestSnapshot{
		Path:          r.URL.Path,
		Authorization: r.Header.Get("Authorization"),
		Env:           r.Header.Get("X-Loza-Env"),
		Service:       r.Header.Get("X-Loza-Service"),
	}
}

func writeLQLTestResult(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"columns": []string{"event_id"}, "rows": []map[string]any{{"event_id": "evt-1"}}, "row_count": 1})
}

func TestQueryLQLUsesScopedRouteAndScopeHeaders(t *testing.T) {
	observed := make(chan lqlRequestSnapshot, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- snapshotLQLRequest(r)
		writeLQLTestResult(w)
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
	// Assertions run on the test goroutine, so Fatalf is safe here and a
	// mismatched request still yields a complete response from the handler.
	got := <-observed
	if got.Path != "/collectors/demo/lql/query" {
		t.Errorf("path = %q", got.Path)
	}
	if got.Authorization != "Bearer api" {
		t.Errorf("authorization = %q", got.Authorization)
	}
	if got.Env != "prod" || got.Service != "cli" {
		t.Errorf("scope headers = %q/%q", got.Env, got.Service)
	}
}

// TestLQLTestHandlerWritesResponseOnRequestMismatch reproduces the failure
// mode from MASS-0436: a request that violates the expected path/headers must
// not make the handler goroutine abort. Previously the handler called
// t.Fatalf, which invoked runtime.Goexit in the handler goroutine and returned
// without writing a body, surfacing only as a confusing EOF at the client.
func TestLQLTestHandlerWritesResponseOnRequestMismatch(t *testing.T) {
	observed := make(chan lqlRequestSnapshot, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- snapshotLQLRequest(r)
		writeLQLTestResult(w)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/wrong/path", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("handler aborted without a response: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("handler wrote no response body")
	}
	var result QueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("handler wrote a non-JSON response: %v", err)
	}
	if result.RowCount != 1 {
		t.Fatalf("row count = %d", result.RowCount)
	}

	// The mismatched request is still captured for assertion on the test
	// goroutine, rather than silently aborting the handler.
	got := <-observed
	if got.Path != "/wrong/path" {
		t.Fatalf("observed path = %q, want %q", got.Path, "/wrong/path")
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
