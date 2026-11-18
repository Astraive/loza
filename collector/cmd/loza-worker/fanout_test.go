package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestValidateFanoutRolesRejectsMultipleEnabledFallbacks(t *testing.T) {
	outputs := []workerFanoutOutput{
		{name: "fb-a", role: fanoutRoleFallback, enabled: true},
		{name: "fb-b", role: fanoutRoleFallback, enabled: true},
	}
	err := validateFanoutRoles(outputs)
	if err == nil {
		t.Fatal("expected an error for multiple enabled fallback outputs")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFanoutRolesAllowsSingleEnabledFallback(t *testing.T) {
	outputs := []workerFanoutOutput{
		{name: "fb-a", role: fanoutRoleFallback, enabled: true},
		{name: "fb-disabled", role: fanoutRoleFallback, enabled: false},
		{name: "sec", role: fanoutRoleSecondary, enabled: true},
	}
	if err := validateFanoutRoles(outputs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Regression for MASS-0143: two fallback outputs must be rejected before any
// sink is created, so the earlier fallback cannot be silently overwritten and
// leak its background flush loop/resources.
func TestCreateFanoutSinksRejectsMultipleFallbacks(t *testing.T) {
	cfg := workerConfig{
		fanoutOutputs: []workerFanoutOutput{
			{name: "fb-a", role: fanoutRoleFallback, enabled: true},
			{name: "fb-b", role: fanoutRoleFallback, enabled: true},
		},
	}
	secondary, fallback, dbs, err := createFanoutSinks(cfg)
	if err == nil {
		t.Fatal("expected createFanoutSinks to reject multiple fallback outputs")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("unexpected error: %v", err)
	}
	if secondary != nil || fallback != nil || dbs != nil {
		t.Fatalf("expected no sinks on validation failure, got secondary=%v fallback=%v dbs=%v", secondary, fallback, dbs)
	}
}

// registrationDriverOnce guards sql.Register, which panics on duplicate names.
var (
	registrationDriverOnce sync.Once
	recordedQueries        []string
)

type lozaRecordingDriver struct{}

type lozaRecordingConn struct{}

func (lozaRecordingDriver) Open(string) (driver.Conn, error) {
	return lozaRecordingConn{}, nil
}

func (lozaRecordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (lozaRecordingConn) Close() error { return nil }

func (lozaRecordingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}

func (lozaRecordingConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	recordedQueries = append(recordedQueries, query)
	return driver.RowsAffected(0), nil
}

func newRecordingDB(t *testing.T) *sql.DB {
	t.Helper()
	registrationDriverOnce.Do(func() {
		sql.Register("loza_fanout_recording", lozaRecordingDriver{})
	})
	recordedQueries = nil
	db, err := sql.Open("loza_fanout_recording", "")
	if err != nil {
		t.Fatalf("open recording db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestEnsureSchemaAcceptsQualifiedTableName reproduces MASS-0142: a table name
// that passes config validation must not be rejected when ensureSchema creates
// the table at worker startup.
func TestEnsureSchemaAcceptsQualifiedTableName(t *testing.T) {
	const table = "main.events"
	if !validWorkerTableName(table) {
		t.Fatalf("validWorkerTableName(%q) = false, want true", table)
	}

	db := newRecordingDB(t)
	cfg := workerConfig{
		duckDBTable:     table,
		duckDBRawColumn: "raw",
		duckDBStoreRaw:  true,
	}
	if err := ensureSchema(db, cfg); err != nil {
		t.Fatalf("ensureSchema with qualified table name failed: %v", err)
	}
	if len(recordedQueries) != 1 {
		t.Fatalf("expected 1 CREATE TABLE query, got %d: %v", len(recordedQueries), recordedQueries)
	}
	query := recordedQueries[0]
	want := `CREATE TABLE IF NOT EXISTS "main"."events" ("raw" TEXT)`
	if query != want {
		t.Fatalf("CREATE TABLE query mismatch\n got: %s\nwant: %s", query, want)
	}
}

func TestQuoteWorkerSQLTableName(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		want    string
		wantErr bool
	}{
		{"unqualified", "events", `"events"`, false},
		{"schema qualified", "main.events", `"main"."events"`, false},
		{"multi part", "db.main.events", `"db"."main"."events"`, false},
		{"whitespace trimmed", "  main.events  ", `"main"."events"`, false},
		{"empty", "", "", true},
		{"empty part", "main..events", "", true},
		{"invalid character", "main.ev;ents", "", true},
		{"injected quote", `main."events"`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := quoteWorkerSQLTableName(tt.table)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("quoteWorkerSQLTableName(%q) = %q, want error", tt.table, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("quoteWorkerSQLTableName(%q): %v", tt.table, err)
			}
			if got != tt.want {
				t.Fatalf("quoteWorkerSQLTableName(%q) = %q, want %q", tt.table, got, tt.want)
			}
		})
	}
}

// Column identifiers must remain strictly unqualified: qualification is only
// valid for table names, matching the DuckDB sink's quoteIdentifier behavior.
func TestQuoteWorkerSQLIdentRejectsQualification(t *testing.T) {
	if _, err := quoteWorkerSQLIdent("main.events"); err == nil {
		t.Fatal("quoteWorkerSQLIdent accepted a qualified column name")
	}
}
