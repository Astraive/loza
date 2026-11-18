package main

import (
	"testing"

	collectorconfig "github.com/astraive/loza/collector/internal/config"
)

func TestWorkerPostgresDSNTrimsEnvVarNames(t *testing.T) {
	t.Setenv("MASS0145_PG_USER", "alice")
	t.Setenv("MASS0145_PG_PASSWORD", "s3cr3t")

	connection := collectorconfig.DatabaseConnectionConfig{
		Host:        "db.internal",
		Port:        5432,
		Database:    "events",
		UsernameEnv: "  MASS0145_PG_USER \t",
		PasswordEnv: "\nMASS0145_PG_PASSWORD  ",
		SSLMode:     "disable",
	}

	got := workerPostgresDSN(connection)
	want := "postgres://alice:s3cr3t@db.internal:5432/events?sslmode=disable"
	if got != want {
		t.Fatalf("workerPostgresDSN() = %q, want %q", got, want)
	}
}
