package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultReturnsErrorOnMissingDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	t.Setenv("LOZA_CLI_DEFAULTS", missing)

	cfg, err := Default()
	if err == nil {
		t.Fatalf("Default() with missing defaults file: expected error, got nil (cfg=%+v)", cfg)
	}
}

func TestDefaultReturnsErrorOnMalformedDefaults(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "loza-cli.defaults.yaml")
	if err := os.WriteFile(defaultsPath, []byte("collector_url: [unterminated\n"), 0o600); err != nil {
		t.Fatalf("write malformed defaults: %v", err)
	}
	t.Setenv("LOZA_CLI_DEFAULTS", defaultsPath)

	cfg, err := Default()
	if err == nil {
		t.Fatalf("Default() with malformed defaults file: expected error, got nil (cfg=%+v)", cfg)
	}
}

func TestDefaultLoadsDefaults(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "loza-cli.defaults.yaml")
	if err := os.WriteFile(defaultsPath, []byte(""+
		"collector_repo_path: defaults-collector\n"+
		"spec_repo_path: defaults-spec\n"+
		"collector_url: http://defaults:9308\n"), 0o600); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
	t.Setenv("LOZA_CLI_DEFAULTS", defaultsPath)

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	if cfg.CollectorRepoPath != "defaults-collector" {
		t.Fatalf("collector repo path = %q, want defaults-collector", cfg.CollectorRepoPath)
	}
	if cfg.SpecRepoPath != "defaults-spec" {
		t.Fatalf("spec repo path = %q, want defaults-spec", cfg.SpecRepoPath)
	}
}

func TestLoadUsesDefaultsThenUserConfig(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "loza-cli.defaults.yaml")
	userPath := filepath.Join(dir, ".loza-cli.yaml")

	if err := os.WriteFile(defaultsPath, []byte(""+
		"collector_repo_path: defaults-collector\n"+
		"spec_repo_path: defaults-spec\n"+
		"collector_url: http://defaults:9308\n"+
		"duckdb_path: defaults.db\n"+
		"spool_dir: defaults-spool\n"+
		"spool_file: defaults.ndjson\n"+
		"dlq_path: defaults-dlq.ndjson\n"), 0o600); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
	if err := os.WriteFile(userPath, []byte("collector_url: http://user:9308\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	t.Setenv("LOZA_CLI_DEFAULTS", defaultsPath)
	t.Setenv("LOZA_CLI_CONFIG", userPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CollectorURL != "http://user:9308" {
		t.Fatalf("collector url = %q, want user override", cfg.CollectorURL)
	}
	if cfg.DuckDBPath != "defaults.db" {
		t.Fatalf("duckdb path = %q, want defaults fallback", cfg.DuckDBPath)
	}
}

func TestLoadRejectsEmptyRequiredPaths(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "empty collector repo path",
			config: "" +
				"collector_repo_path: \"\"\n" +
				"spec_repo_path: defaults-spec\n" +
				"collector_url: http://defaults:9308\n",
			wantErr: "collector_repo_path must not be empty",
		},
		{
			name: "empty spec repo path",
			config: "" +
				"collector_repo_path: defaults-collector\n" +
				"spec_repo_path: \"\"\n" +
				"collector_url: http://defaults:9308\n",
			wantErr: "spec_repo_path must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			defaultsPath := filepath.Join(dir, "loza-cli.defaults.yaml")

			if err := os.WriteFile(defaultsPath, []byte(tt.config), 0o600); err != nil {
				t.Fatalf("write defaults: %v", err)
			}

			t.Setenv("LOZA_CLI_DEFAULTS", defaultsPath)
			t.Setenv("LOZA_CLI_CONFIG", filepath.Join(dir, ".loza-cli.yaml"))

			cfg, err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded with cfg=%+v, want validation error for empty required path", cfg)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadNormalizesValidationMode(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "loza-cli.defaults.yaml")
	userPath := filepath.Join(dir, ".loza-cli.yaml")

	if err := os.WriteFile(defaultsPath, []byte(""+
		"collector_repo_path: defaults-collector\n"+
		"spec_repo_path: defaults-spec\n"+
		"collector_url: http://defaults:9308\n"+
		"validation_mode: enforce\n"), 0o600); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
	if err := os.WriteFile(userPath, []byte("validation_mode: \"  ENFORCE  \"\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	t.Setenv("LOZA_CLI_DEFAULTS", defaultsPath)
	t.Setenv("LOZA_CLI_CONFIG", userPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ValidationMode != "enforce" {
		t.Fatalf("validation mode = %q, want normalized %q", cfg.ValidationMode, "enforce")
	}
}
