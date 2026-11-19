package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/database"
	publichttp "github.com/astraive/loza/collector/server/http"
)

type fakeDatabaseConnection struct {
	info      database.Metadata
	lastLimit int
	queries   int
}

func (f *fakeDatabaseConnection) Backend() string             { return f.info.Backend }
func (f *fakeDatabaseConnection) Metadata() database.Metadata { return f.info }
func (f *fakeDatabaseConnection) Query(ctx context.Context, query string, args ...any) (database.Result, error) {
	return f.QueryWithLimit(ctx, 0, query, args...)
}
func (f *fakeDatabaseConnection) QueryWithLimit(_ context.Context, limit int, _ string, _ ...any) (database.Result, error) {
	f.lastLimit = limit
	f.queries++
	return database.Result{Columns: []string{"value"}, Rows: [][]any{{"ok"}}}, nil
}
func (f *fakeDatabaseConnection) Ping(context.Context) error  { return nil }
func (f *fakeDatabaseConnection) Close(context.Context) error { return nil }

type fakeLQLCompiler struct {
	planLimit int
}

func (f fakeLQLCompiler) Compile(_ context.Context, req LQLCompileRequest) (ParameterizedPlan, error) {
	limit := f.planLimit
	if limit == 0 {
		limit = req.Limit
	}
	return ParameterizedPlan{SQL: "SELECT value", Args: []any{}, Limit: limit}, nil
}
func (fakeLQLCompiler) Close(context.Context) error { return nil }
func TestDatabaseConnectionsMetadataIsSanitized(t *testing.T) {
	connection := &fakeDatabaseConnection{info: database.Metadata{
		Name: "analytics", Backend: "postgres", Host: "db.internal", Port: 5432,
		Database: "loza", Table: "events", Enabled: true, Capabilities: []string{"query"},
	}}
	state := &collectorState{

		cfg:                 collectorConfig{authEnabled: false},
		databaseConnections: map[string]database.Connection{"analytics": connection},
		databaseMetadata:    map[string]database.Metadata{"analytics": connection.Metadata()},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/collectors/acme/database/connections", nil)
	request = request.WithContext(publichttp.WithAuthorizedCollector(request.Context(), "acme", "prod"))
	state.HandleDatabaseConnections(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metadata status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if body == "" || containsSecretField(body) {
		t.Fatalf("metadata response leaked secret fields: %s", body)
	}
}

func TestDatabaseConnectionsIncludesLegacyQueryConnectionWithNamedConnections(t *testing.T) {
	named := &fakeDatabaseConnection{info: database.Metadata{Name: "analytics", Backend: "postgres"}}
	legacy := &fakeDatabaseConnection{info: database.Metadata{Name: "primary", Backend: "duckdb"}}
	state := &collectorState{
		cfg:                 collectorConfig{authEnabled: false},
		databaseConnections: map[string]database.Connection{"analytics": named},
		databaseMetadata:    map[string]database.Metadata{"analytics": named.Metadata()},
		queryConnection:     legacy,
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/collectors/acme/database/connections", nil)
	request = request.WithContext(publichttp.WithAuthorizedCollector(request.Context(), "acme", "prod"))
	state.HandleDatabaseConnections(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metadata status = %d", recorder.Code)
	}
	var payload struct {
		Connections []databaseConnectionResponse `json:"connections"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode connections: %v (body=%s)", err, recorder.Body.String())
	}
	names := make(map[string]int, len(payload.Connections))
	for _, entry := range payload.Connections {
		names[entry.Name]++
	}
	if names["analytics"] != 1 {
		t.Fatalf("named connection missing or duplicated: %s", recorder.Body.String())
	}
	if names["primary"] != 1 {
		t.Fatalf("legacy queryConnection hidden when named connection exists: %s", recorder.Body.String())
	}
}

func TestDatabaseConnectionsDoesNotDuplicateNamedStorageConnection(t *testing.T) {
	storage := &fakeDatabaseConnection{info: database.Metadata{Name: "analytics", Backend: "postgres"}}
	state := &collectorState{
		cfg:                 collectorConfig{authEnabled: false, storageConnection: "analytics"},
		databaseConnections: map[string]database.Connection{"analytics": storage},
		databaseMetadata:    map[string]database.Metadata{"analytics": storage.Metadata()},
		queryConnection:     storage, // storage.connection points at the named connection
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/collectors/acme/database/connections", nil)
	request = request.WithContext(publichttp.WithAuthorizedCollector(request.Context(), "acme", "prod"))
	state.HandleDatabaseConnections(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metadata status = %d", recorder.Code)
	}
	var payload struct {
		Connections []databaseConnectionResponse `json:"connections"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode connections: %v (body=%s)", err, recorder.Body.String())
	}
	if len(payload.Connections) != 1 || payload.Connections[0].Name != "analytics" {
		t.Fatalf("storage connection duplicated or renamed: %s", recorder.Body.String())
	}
}

func TestDatabaseQueryUsesNamedConnection(t *testing.T) {
	connection := &fakeDatabaseConnection{info: database.Metadata{Name: "analytics", Backend: "postgres"}}
	state := &collectorState{
		cfg: collectorConfig{
			authEnabled:         false,
			databaseConnections: []databaseConnectionConfig{{name: "analytics", queryTimeout: time.Second}},
		},
		databaseConnections: map[string]database.Connection{"analytics": connection},
		lqlCompiler:         fakeLQLCompiler{},
	}
	request := httptest.NewRequest(http.MethodPost, "/collectors/acme/database/query", strings.NewReader(`{"connection":"analytics","query":"from events | take 1","limit":1}`))
	request = request.WithContext(publichttp.WithAuthorizedCollector(request.Context(), "acme", "prod"))
	recorder := httptest.NewRecorder()
	state.HandleDatabaseQuery(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"backend":"postgres"`) {
		t.Fatalf("named query failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDatabaseQueryThreadsPlanLimitToCollector(t *testing.T) {
	connection := &fakeDatabaseConnection{info: database.Metadata{Name: "analytics", Backend: "postgres"}}
	state := &collectorState{
		cfg: collectorConfig{
			authEnabled:         false,
			databaseConnections: []databaseConnectionConfig{{name: "analytics", queryTimeout: time.Second}},
		},
		databaseConnections: map[string]database.Connection{"analytics": connection},
		// The compiler enforces a tighter bound than the request asked for;
		// the collector must receive the plan's limit, not zero.
		lqlCompiler: fakeLQLCompiler{planLimit: 3},
	}
	request := httptest.NewRequest(http.MethodPost, "/collectors/acme/database/query", strings.NewReader(`{"connection":"analytics","query":"from events","limit":50}`))
	request = request.WithContext(publichttp.WithAuthorizedCollector(request.Context(), "acme", "prod"))
	recorder := httptest.NewRecorder()
	state.HandleDatabaseQuery(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("query status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if connection.queries != 1 {
		t.Fatalf("expected one collector query, got %d", connection.queries)
	}
	if connection.lastLimit != 3 {
		t.Fatalf("collector limit = %d, want plan limit 3", connection.lastLimit)
	}
}

func containsSecretField(body string) bool {
	return stringContains(body, "password") || stringContains(body, "username") || stringContains(body, "dsn")
}

func stringContains(body, needle string) bool {
	for i := 0; i+len(needle) <= len(body); i++ {
		if body[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
