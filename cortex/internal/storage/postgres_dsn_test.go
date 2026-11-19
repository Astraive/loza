package storage

import (
	"net/url"
	"strings"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
)

func remotePostgresConfig() config.PostgresConfig {
	return config.PostgresConfig{
		Host:     "db.example.internal",
		Port:     5432,
		Database: "cortex",
		User:     "cortex",
		Password: "s3cr3t",
		SSLMode:  "disable",
	}
}

func parseDSN(t *testing.T, dsn string) *url.URL {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("failed to parse DSN %q: %v", dsn, err)
	}
	return u
}

func TestBuildPostgresDSNLocalHostKeepsConfiguredMode(t *testing.T) {
	cfg := remotePostgresConfig()
	cfg.Host = "localhost"
	cfg.SSLMode = "disable"

	dsn, err := buildPostgresDSN(cfg)
	if err != nil {
		t.Fatalf("local host should allow sslmode=disable: %v", err)
	}
	if got := parseDSN(t, dsn).Query().Get("sslmode"); got != "disable" {
		t.Fatalf("expected sslmode=disable for local host, got %q", got)
	}
}

func TestBuildPostgresDSNLocalHostDetectedByAddress(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "", "/var/run/postgresql"} {
		cfg := remotePostgresConfig()
		cfg.Host = host

		if _, err := buildPostgresDSN(cfg); err != nil {
			t.Fatalf("host %q should be treated as local: %v", host, err)
		}
	}
}

func TestBuildPostgresDSNRemoteDisableRequiresCA(t *testing.T) {
	cfg := remotePostgresConfig() // SSLMode: disable, no CA

	_, err := buildPostgresDSN(cfg)
	if err == nil {
		t.Fatal("expected an error for a remote host with sslmode=disable and no CA")
	}
	if !strings.Contains(err.Error(), "sslrootcert") {
		t.Fatalf("error should mention the missing CA certificate, got: %v", err)
	}
}

func TestBuildPostgresDSNRemoteUpgradesInsecureModes(t *testing.T) {
	for _, mode := range []string{"", "disable", "allow", "prefer", "require", "verify-ca"} {
		t.Run(mode, func(t *testing.T) {
			cfg := remotePostgresConfig()
			cfg.SSLMode = mode
			cfg.SSLRootCert = "/etc/ssl/certs/db-ca.pem"

			dsn, err := buildPostgresDSN(cfg)
			if err != nil {
				t.Fatalf("unexpected error for sslmode %q: %v", mode, err)
			}
			q := parseDSN(t, dsn).Query()
			if got := q.Get("sslmode"); got != "verify-full" {
				t.Fatalf("sslmode %q should be upgraded to verify-full, got %q", mode, got)
			}
			if got := q.Get("sslrootcert"); got != cfg.SSLRootCert {
				t.Fatalf("expected sslrootcert %q, got %q", cfg.SSLRootCert, got)
			}
		})
	}
}

func TestBuildPostgresDSNRemoteKeepsVerifyFull(t *testing.T) {
	cfg := remotePostgresConfig()
	cfg.SSLMode = "verify-full"
	cfg.SSLRootCert = "/etc/ssl/certs/db-ca.pem"

	dsn, err := buildPostgresDSN(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := parseDSN(t, dsn).Query().Get("sslmode"); got != "verify-full" {
		t.Fatalf("expected verify-full, got %q", got)
	}
}

func TestBuildPostgresDSNRejectsInvalidSSLMode(t *testing.T) {
	for _, host := range []string{"localhost", "db.example.internal"} {
		cfg := remotePostgresConfig()
		cfg.Host = host
		cfg.SSLMode = "totally-invalid"

		if _, err := buildPostgresDSN(cfg); err == nil {
			t.Fatalf("host %q: expected an error for an invalid sslmode", host)
		}
	}
}

func TestBuildPostgresDSNDoesNotInterpolateRawPassword(t *testing.T) {
	cfg := remotePostgresConfig()
	cfg.Password = "p@ss w/rd:#!"
	cfg.SSLRootCert = "/etc/ssl/certs/db-ca.pem"

	dsn, err := buildPostgresDSN(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(dsn, cfg.Password) {
		t.Fatalf("raw password must not appear in the DSN: %q", dsn)
	}
	u := parseDSN(t, dsn)
	if u.User == nil {
		t.Fatal("DSN is missing user credentials")
	}
	got, ok := u.User.Password()
	if !ok || got != cfg.Password {
		t.Fatalf("expected structured password to round-trip, got %q (present=%v)", got, ok)
	}
}

func TestNewPostgresStorageRejectsInsecureRemoteConfigBeforeConnecting(t *testing.T) {
	cfg := remotePostgresConfig() // remote host, sslmode=disable, no CA

	_, err := NewPostgresStorage(cfg)
	if err == nil {
		t.Fatal("expected NewPostgresStorage to reject an insecure remote configuration")
	}
	if !strings.Contains(err.Error(), "invalid postgres configuration") ||
		!strings.Contains(err.Error(), "sslrootcert") {
		t.Fatalf("unexpected error: %v", err)
	}
}
