package main

import (
	"path/filepath"
	"testing"
)

// The local config is mounted by every example/deploy compose file and by the
// quickstart `docker run` snippet. It must load on a fresh clone without any
// API-key or storage secrets beyond the documented dev encryption key.
func TestLocalExampleConfigLoadsWithoutCredentials(t *testing.T) {
	for _, key := range []string{
		"COLLECTOR_AUTH_SERVER_SECRET",
		"COLLECTOR_INGEST_KEY_SECRET",
		"COLLECTOR_ADMIN_KEY_SECRET",
		"COLLECTOR_API_KEY",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("LOZA_STORAGE_ENCRYPTION_KEY", "dev-only-not-for-production")

	path := filepath.Join("..", "..", "configs", "loza.local.yaml")
	cfg, err := loadCollectorConfigFromArgs([]string{"-c", path})
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if cfg.authEnabled {
		t.Fatal("local example config must not require API keys")
	}
	for name, got := range map[string]string{
		"ingest":  cfg.ingestPath,
		"health":  cfg.healthPath,
		"ready":   cfg.readyPath,
		"metrics": cfg.metricsPath,
	} {
		want := map[string]string{
			"ingest":  "/events",
			"health":  "/health",
			"ready":   "/readyz",
			"metrics": "/metrics",
		}[name]
		if got != want {
			t.Errorf("route %s = %q, want %q", name, got, want)
		}
	}
}
