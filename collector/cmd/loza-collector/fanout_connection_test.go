package main

import (
	"reflect"
	"testing"
)

// Regression test for MASS-0102: a connection marked enabled: false must not
// supply path/DSN/hosts or credentials to a fanout output that references it.
func TestApplyFanoutConnectionRejectsDisabledConnection(t *testing.T) {
	tests := []struct {
		name       string
		connection databaseConnectionConfig
		output     collectorFanoutOutput
	}{
		{
			name: "duckdb",
			connection: databaseConnectionConfig{
				name: "disabled", backend: fanoutSinkDuckDB, enabled: false,
				path: "/disabled.duckdb", table: "events", rawColumn: "raw", storeRaw: true,
			},
			output: collectorFanoutOutput{name: "out", connection: "disabled", sinkType: fanoutSinkDuckDB},
		},
		{
			name: "postgres",
			connection: databaseConnectionConfig{
				name: "disabled", backend: fanoutSinkPostgres, enabled: false,
				host: "db.internal", port: 5432, database: "events", username: "user", password: "secret",
				table: "events", rawColumn: "raw", storeRaw: true,
			},
			output: collectorFanoutOutput{name: "out", connection: "disabled", sinkType: fanoutSinkPostgres},
		},
		{
			name: "clickhouse",
			connection: databaseConnectionConfig{
				name: "disabled", backend: fanoutSinkClickHouse, enabled: false,
				hosts: []string{"ch.internal:9000"}, database: "events", username: "user", password: "secret",
				table: "events", rawColumn: "raw", storeRaw: true,
			},
			output: collectorFanoutOutput{name: "out", connection: "disabled", sinkType: fanoutSinkClickHouse},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := collectorConfig{databaseConnections: []databaseConnectionConfig{tt.connection}}
			output := tt.output
			before := output

			err := applyFanoutConnection(&output, cfg)
			if err == nil {
				t.Fatalf("expected an error for disabled %s connection, got nil", tt.name)
			}
			if !reflect.DeepEqual(output, before) {
				t.Fatalf("disabled %s connection was applied to output: before=%+v after=%+v", tt.name, before, output)
			}
		})
	}
}

func TestApplyFanoutConnectionAppliesEnabledConnection(t *testing.T) {
	cfg := collectorConfig{databaseConnections: []databaseConnectionConfig{{
		name: "active", backend: fanoutSinkDuckDB, enabled: true,
		path: "/active.duckdb", table: "events", rawColumn: "raw", storeRaw: true,
	}}}
	output := collectorFanoutOutput{name: "out", connection: "active", sinkType: fanoutSinkDuckDB}

	if err := applyFanoutConnection(&output, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.duckDBPath != "/active.duckdb" || output.duckDBTable != "events" || output.duckDBRawColumn != "raw" {
		t.Fatalf("enabled connection fields were not applied: %+v", output)
	}
	if output.duckDBStoreRaw == nil || !*output.duckDBStoreRaw {
		t.Fatalf("enabled connection store_raw was not applied: %+v", output)
	}
}

func TestApplyFanoutConnectionSkipsDisabledAndFindsEnabled(t *testing.T) {
	cfg := collectorConfig{databaseConnections: []databaseConnectionConfig{
		{name: "disabled", backend: fanoutSinkDuckDB, enabled: false, path: "/disabled.duckdb"},
		{name: "active", backend: fanoutSinkDuckDB, enabled: true, path: "/active.duckdb"},
	}}
	output := collectorFanoutOutput{name: "out", connection: "active"}

	if err := applyFanoutConnection(&output, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.duckDBPath != "/active.duckdb" {
		t.Fatalf("expected enabled connection path, got %q", output.duckDBPath)
	}
}
