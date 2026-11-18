package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

func duckDBSchemaTestItem(t *testing.T, cfg collectorConfig) databaseConnectionConfig {
	t.Helper()
	return databaseConnectionConfig{
		name:              "test-duckdb",
		backend:           "duckdb",
		enabled:           true,
		path:              filepath.Join(t.TempDir(), "schema.db"),
		driver:            "duckdb",
		table:             cfg.duckDBTable,
		rawColumn:         cfg.duckDBRawColumn,
		storeRaw:          cfg.duckDBStoreRaw,
		schema:            cfg.duckDBSchema,
		columnTypes:       cfg.duckDBColumnTypes,
		connectionTimeout: 5 * time.Second,
	}
}

func TestEnsureSchemaContextHonorsCancellation(t *testing.T) {
	cfg := testCollectorConfig()
	item := duckDBSchemaTestItem(t, cfg)
	schemaCfg := databaseSchemaConfig(cfg, item)

	db, err := sql.Open(item.driver, item.path)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	// A live context must still initialize the schema normally.
	if err := ensureSchemaContext(context.Background(), db, schemaCfg); err != nil {
		t.Fatalf("ensureSchemaContext with live context: %v", err)
	}

	// A cancelled context must abort schema initialization instead of
	// running the statements unbounded (the reported defect).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ensureSchemaContext(ctx, db, schemaCfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("ensureSchemaContext with cancelled context: got %v, want context.Canceled", err)
	}
}

func TestOpenNamedDuckDBPropagatesContextToSchemaInit(t *testing.T) {
	cfg := testCollectorConfig()
	item := duckDBSchemaTestItem(t, cfg)

	// A cancelled context reaching openNamedDuckDB must abort schema
	// initialization, proving the connection-timeout context is not dropped.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := openNamedDuckDB(ctx, cfg, item)
	if err == nil {
		_ = conn.Close(context.Background())
		t.Fatal("openNamedDuckDB with cancelled context succeeded; schema init ignored the context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("openNamedDuckDB with cancelled context: got %v, want context.Canceled", err)
	}
}
