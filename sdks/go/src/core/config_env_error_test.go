package core

import (
	"strings"
	"testing"
)

func clearEnvConfig(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LOZA_DSN",
		"LOZA_COLLECTOR_URL",
		"LOZA_API_KEY",
		"LOZA_SERVICE_NAME",
		"LOZA_SERVICE_VERSION",
		"LOZA_ENVIRONMENT",
		"LOZA_TENANT_ID",
		"LOZA_BATCH_SIZE",
		"LOZA_FLUSH_INTERVAL",
		"LOZA_MAX_BUFFER_SIZE",
		"LOZA_MAX_RETRIES",
		"LOZA_MAX_BACKOFF",
		"LOZA_TIMEOUT",
		"LOZA_CONNECTION_TIMEOUT",
		"LOZA_ENABLE_COMPRESSION",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadFromEnvESurfacesParseErrors(t *testing.T) {
	clearEnvConfig(t)
	t.Setenv("LOZA_BATCH_SIZE", "not-an-int")
	t.Setenv("LOZA_FLUSH_INTERVAL", "bogus")
	t.Setenv("LOZA_ENABLE_COMPRESSION", "maybe")

	_, err := LoadFromEnvE(Config{})
	if err == nil {
		t.Fatal("LoadFromEnvE should report malformed environment values")
	}
	for _, name := range []string{"LOZA_BATCH_SIZE", "LOZA_FLUSH_INTERVAL", "LOZA_ENABLE_COMPRESSION"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not mention %s", err, name)
		}
	}
}

func TestLoadFromEnvKeepsBackwardCompatibleSignature(t *testing.T) {
	clearEnvConfig(t)
	t.Setenv("LOZA_SERVICE_NAME", "env-service")

	cfg := LoadFromEnv(Config{})
	if cfg.Service != "env-service" {
		t.Fatalf("Service = %q, want env-service", cfg.Service)
	}
}

func TestNewClientSurfacesEnvConfigErrors(t *testing.T) {
	clearEnvConfig(t)
	t.Setenv("LOZA_MAX_BUFFER_SIZE", "not-an-int")

	cfg := Test()
	cfg.Service = "svc"
	cfg.CollectorURL = "https://collector.example.com"
	_, err := NewClient(cfg)
	if err == nil {
		t.Fatal("NewClient should surface malformed environment configuration")
	}
	if !strings.Contains(err.Error(), "LOZA_MAX_BUFFER_SIZE") {
		t.Fatalf("error %q does not mention LOZA_MAX_BUFFER_SIZE", err)
	}
}
