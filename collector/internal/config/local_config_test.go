package config

import (
	"path/filepath"
	"testing"
)

// loza.local.yaml is mounted by every example/deploy compose file and by the
// quickstart `docker run` snippet. It must be a single, strictly-parsable doc.
func TestLocalExampleConfigIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "loza.local.yaml")
	cfg := Default()
	if err := LoadFile(&cfg, path); err != nil {
		t.Fatalf("load %s: %v", path, err)
	}

	for name, got := range map[string]string{
		"ingest":  cfg.Routes.Ingest,
		"health":  cfg.Routes.Health,
		"ready":   cfg.Routes.Ready,
		"metrics": cfg.Routes.Metrics,
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
	if cfg.Auth.Enabled {
		t.Error("local example config must not require API keys")
	}
	if len(cfg.Auth.Keys) != 0 {
		t.Errorf("local example config has %d configured keys, want 0", len(cfg.Auth.Keys))
	}
	if cfg.Storage.EncryptionKeyEnv != "LOZA_STORAGE_ENCRYPTION_KEY" {
		t.Errorf("encryption_key_env = %q, want LOZA_STORAGE_ENCRYPTION_KEY", cfg.Storage.EncryptionKeyEnv)
	}
}
