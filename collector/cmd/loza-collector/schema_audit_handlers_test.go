package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
)

func newBlueprintTestState(t *testing.T) (*collectorState, *sql.DB) {
	t.Helper()
	cfg := testCollectorConfig()
	cfg.duckDBDriver = "duckdb"
	cfg.duckDBTable = "events"
	db, err := sql.Open(cfg.duckDBDriver, filepath.Join(t.TempDir(), "blueprint.db"))
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return &collectorState{cfg: cfg, queryDB: db}, db
}

func columnExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'events' AND column_name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("query column %q: %v", name, err)
	}
	return count > 0
}

// TestApplyBlueprintRollsBackOnMidLoopError verifies that a failure partway
// through adding blueprint columns does not leave earlier columns applied.
// "DECIMAL(999)" passes applyBlueprint's type validation (digits-only params)
// but is rejected by DuckDB at execution time, forcing a deterministic
// mid-loop failure.
func TestApplyBlueprintRollsBackOnMidLoopError(t *testing.T) {
	state, db := newBlueprintTestState(t)

	bp := Blueprint{
		Name: "partial",
		Columns: map[string]BlueprintColumn{
			"good_col": {JSONPath: "$.good", DuckDBType: "INTEGER"},
			"bad_col":  {JSONPath: "$.bad", DuckDBType: "DECIMAL(999)"},
		},
	}
	if err := state.applyBlueprint(bp); err == nil {
		t.Fatal("expected applyBlueprint to fail on the invalid DECIMAL(999) column")
	}
	if columnExists(t, db, "good_col") {
		t.Fatal("good_col was left applied after a mid-loop failure; blueprint was only partially applied")
	}
}

// TestApplyBlueprintCommitsAllColumnsOnSuccess verifies the transaction is
// committed when every ALTER TABLE succeeds.
func TestApplyBlueprintCommitsAllColumnsOnSuccess(t *testing.T) {
	state, db := newBlueprintTestState(t)

	bp := Blueprint{
		Name: "full",
		Columns: map[string]BlueprintColumn{
			"col_a": {JSONPath: "$.a", DuckDBType: "INTEGER"},
			"col_b": {JSONPath: "$.b", DuckDBType: "TEXT"},
			"col_c": {JSONPath: "$.c", DuckDBType: "DECIMAL(10,2)"},
		},
	}
	if err := state.applyBlueprint(bp); err != nil {
		t.Fatalf("applyBlueprint: %v", err)
	}
	for _, name := range []string{"col_a", "col_b", "col_c"} {
		if !columnExists(t, db, name) {
			t.Fatalf("expected column %q to be applied after successful commit", name)
		}
	}
}
