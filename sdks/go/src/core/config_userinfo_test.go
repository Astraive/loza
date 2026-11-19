package core

import (
	"strings"
	"testing"
)

// TestNewClientRejectsEmbeddedCollectorCredentials guards the main SDK entry
// point against silently dropping userinfo embedded in the collector endpoint.
// safeCollectorEndpoint strips that userinfo, so validation must observe the
// raw endpoint before it is sanitized.
func TestNewClientRejectsEmbeddedCollectorCredentials(t *testing.T) {
	const rawEndpoint = "https://user:secret@collector.example.com:9308"
	const wantErr = "not embedded in the endpoint URL"

	// Ensure no DSN-derived credentials mask the embedded userinfo.
	t.Setenv("LOZA_DSN", "")

	assertRejected := func(t *testing.T, cfg Config) {
		t.Helper()
		_, err := NewClient(cfg)
		if err == nil {
			t.Fatalf("NewClient() accepted collector URL %q with embedded credentials; want an error", rawEndpoint)
		}
		if !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("NewClient() error = %q; want error containing %q", err, wantErr)
		}
	}

	t.Run("from environment", func(t *testing.T) {
		t.Setenv("LOZA_COLLECTOR_URL", rawEndpoint)
		assertRejected(t, Config{Service: "test-service"})
	})

	t.Run("from code config", func(t *testing.T) {
		t.Setenv("LOZA_COLLECTOR_URL", "")
		assertRejected(t, Config{CollectorURL: rawEndpoint, Service: "test-service"})
	})
}
