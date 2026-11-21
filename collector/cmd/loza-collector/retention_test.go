package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
)

// openRetentionTestDB creates an in-memory DuckDB with the supplied schema and
// a seed row, so aggregate queries return a non-NULL value.
func openRetentionTestDB(t *testing.T, schema, seed string) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open retention test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create retention test table: %v", err)
	}
	if _, err := db.Exec(seed); err != nil {
		t.Fatalf("seed retention test table: %v", err)
	}
	return db
}

func newRetentionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE events (
		event_id VARCHAR,
		timestamp TIMESTAMP,
		raw BLOB
	)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}
	return db
}

func TestExecuteRetentionPropagatesDeleteErrors(t *testing.T) {
	tests := []struct {
		name          string
		schema        string
		seed          string
		cfg           collectorConfig
		wantFragments []string
		wantErr       bool
	}{
		{
			name:   "age delete failure is returned",
			schema: `CREATE TABLE events (event_id VARCHAR, raw BLOB)`,
			seed:   `INSERT INTO events (event_id, raw) VALUES ('evt1', '{}'::BLOB)`,
			cfg: collectorConfig{
				duckDBTable: "events", retentionDays: 1, retentionMaxSize: 1000000,
			},
			wantFragments: []string{"delete events by age", "timestamp"},
			wantErr:       true,
		},
		{
			name:   "size delete failure is returned",
			schema: `CREATE TABLE events (event_id VARCHAR, timestamp VARCHAR)`,
			seed:   `INSERT INTO events (event_id, timestamp) VALUES ('evt1', '2020-01-01T00:00:00Z')`,
			cfg: collectorConfig{
				duckDBTable: "events", retentionDays: 1, retentionMaxSize: 1,
			},
			wantFragments: []string{"delete events by size", "raw"},
			wantErr:       true,
		},
		{
			name:   "both delete failures are aggregated",
			schema: `CREATE TABLE events (event_id VARCHAR)`,
			seed:   `INSERT INTO events (event_id) VALUES ('evt1')`,
			cfg: collectorConfig{
				duckDBTable: "events", retentionDays: 1, retentionMaxSize: 1,
			},
			wantFragments: []string{"delete events by age", "delete events by size"},
			wantErr:       true,
		},
		{
			name:   "successful retention returns nil",
			schema: `CREATE TABLE events (event_id VARCHAR, timestamp VARCHAR, raw BLOB)`,
			seed:   `INSERT INTO events (event_id, timestamp, raw) VALUES ('old', '2020-01-01T00:00:00Z', '{}'::BLOB), ('new', '2099-01-01T00:00:00Z', '{}'::BLOB)`,
			cfg: collectorConfig{
				duckDBTable: "events", retentionDays: 1, retentionMaxSize: 1000000,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &collectorState{cfg: tc.cfg, queryDB: openRetentionTestDB(t, tc.schema, tc.seed)}
			err := state.executeRetention()
			if tc.wantErr && err == nil {
				t.Fatal("executeRetention() returned nil; want propagated delete error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("executeRetention() returned unexpected error: %v", err)
			}
			if err == nil {
				return
			}
			for _, fragment := range tc.wantFragments {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("executeRetention() error %q does not contain %q", err, fragment)
				}
			}
		})
	}
}

func TestHandleRetentionApplyReportsDeleteFailure(t *testing.T) {
	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:   "events",
			retentionDays: 1,
		},
		queryDB: openRetentionTestDB(t,
			`CREATE TABLE events (event_id VARCHAR, raw BLOB)`,
			`INSERT INTO events (event_id, raw) VALUES ('evt1', '{}'::BLOB)`,
		),
	}

	rec := httptest.NewRecorder()
	state.handleRetentionApply(rec, httptest.NewRequest(http.MethodPost, "/retention/apply", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("handleRetentionApply() status = %d, want %d (body: %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["applied"] != false {
		t.Errorf("response applied = %v, want false", body["applied"])
	}
	errText, _ := body["error"].(string)
	if !strings.Contains(errText, "delete events by age") {
		t.Errorf("response error = %q, want propagated age delete error", errText)
	}
}

// TestDeleteBySizeEmptyTable reproduces the empty-table case: SUM over an
// empty table returns NULL, which must not be reported as a scan error.
func TestDeleteBySizeEmptyTable(t *testing.T) {
	db := newRetentionTestDB(t)
	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:      "events",
			retentionMaxSize: 1024,
		},
		queryDB: db,
	}

	if err := state.deleteBySize(context.Background(), db); err != nil {
		t.Fatalf("deleteBySize on empty table returned error: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after empty-table retention, got %d", count)
	}
}

// TestDeleteBySizeOverLimit ensures the nullable-SUM fix still performs a
// deletion (without error) when the stored size exceeds the configured limit.
func TestDeleteBySizeOverLimit(t *testing.T) {
	db := newRetentionTestDB(t)
	rows := []struct {
		eventID   string
		timestamp string
	}{
		{"evt-old", "2024-01-01 00:00:00"},
		{"evt-middle", "2024-01-02 00:00:00"},
		{"evt-new", "2024-01-03 00:00:00"},
	}
	payload := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	for _, row := range rows {
		if _, err := db.Exec(
			`INSERT INTO events (event_id, timestamp, raw) VALUES (?, CAST(? AS TIMESTAMP), ?)`,
			row.eventID, row.timestamp, payload,
		); err != nil {
			t.Fatalf("insert test data: %v", err)
		}
	}

	state := &collectorState{
		cfg: collectorConfig{
			duckDBTable:      "events",
			retentionMaxSize: 150,
		},
		queryDB: db,
	}

	if err := state.deleteBySize(context.Background(), db); err != nil {
		t.Fatalf("deleteBySize over limit returned error: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count >= 3 {
		t.Fatalf("expected at least one row to be deleted over the limit, got %d rows remaining", count)
	}
}
