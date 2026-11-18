package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/collector/internal/auth"
	publichttp "github.com/astraive/loza/collector/server/http"
	_ "github.com/marcboeker/go-duckdb"
)

// legacyScopedRequest models the legacy /query route: no collector path value,
// but the router has already attached the configured default collector scope.
func legacyScopedRequest(collector, environment, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	return req.WithContext(publichttp.WithAuthorizedCollector(req.Context(), collector, environment))
}

func queryErrorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return response
}

func TestLegacyQueryRejectsRawSQL(t *testing.T) {
	state := &collectorState{cfg: testCollectorConfig()}
	for _, body := range []string{
		`{"query":"SELECT * FROM events"}`,
		`{"sql":"SELECT * FROM events"}`,
	} {
		rec := httptest.NewRecorder()
		state.handleQuery(rec, legacyScopedRequest("collector-a", "production", body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("raw SQL %s: status = %d, want %d", body, rec.Code, http.StatusBadRequest)
		}
		if got := queryErrorBody(t, rec)["error"]; got != "scoped_raw_sql_unsupported" {
			t.Fatalf("raw SQL %s: error = %#v, want scoped_raw_sql_unsupported", body, got)
		}
	}
}

func TestLegacyQueryRequiresCollectorScope(t *testing.T) {
	state := &collectorState{cfg: testCollectorConfig()}
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"limit":5}`))
	rec := httptest.NewRecorder()

	state.handleQuery(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if got := queryErrorBody(t, rec)["error"]; got != "collector_scope_required" {
		t.Fatalf("error = %#v, want collector_scope_required", got)
	}
}

func TestLegacyQueryReadsOnlyAuthorizedOwnership(t *testing.T) {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (event_id VARCHAR, collector VARCHAR, environment VARCHAR)`); err != nil {
		t.Fatalf("create events table: %v", err)
	}
	for _, row := range [][3]string{{"a-1", "collector-a", "production"}, {"b-1", "collector-b", "production"}} {
		if _, err := db.Exec(`INSERT INTO events VALUES (?, ?, ?)`, row[0], row[1], row[2]); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	state := &collectorState{cfg: collectorConfig{duckDBTable: "events"}, queryDB: db}

	rec := httptest.NewRecorder()
	state.handleQuery(rec, legacyScopedRequest("collector-a", "production", `{"limit":5}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "b-1") || !strings.Contains(body, "a-1") {
		t.Fatalf("legacy query leaked or omitted rows: %s", body)
	}
}

func TestLegacyQueryEnforcesReadPermission(t *testing.T) {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (event_id VARCHAR, collector VARCHAR, environment VARCHAR)`); err != nil {
		t.Fatalf("create events table: %v", err)
	}
	cfg := testCollectorConfig()
	cfg.authEnabled = true
	cfg.duckDBTable = "events"

	for _, tc := range []struct {
		name       string
		permission auth.Permission
		wantStatus int
	}{
		{name: "missing_permission", permission: auth.PermEventsWrite, wantStatus: http.StatusForbidden},
		{name: "read_permission", permission: auth.PermEventsRead, wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &collectorState{cfg: cfg, queryDB: db}
			req := legacyScopedRequest("collector-a", "production", `{"limit":5}`)
			ac := &auth.AuthContext{Permissions: map[auth.Permission]bool{tc.permission: true}}
			req = req.WithContext(auth.WithAuthContext(req.Context(), ac))
			rec := httptest.NewRecorder()

			state.handleQuery(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden {
				if got := queryErrorBody(t, rec)["error"]; got != "query_permission_required" {
					t.Fatalf("error = %#v, want query_permission_required", got)
				}
			}
		})
	}
}
