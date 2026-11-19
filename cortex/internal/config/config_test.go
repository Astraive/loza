package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	if cfg.Server.Port != 9312 {
		t.Fatalf("expected server port 9312, got %d", cfg.Server.Port)
	}
	if cfg.Storage.Backend != "duckdb" {
		t.Fatalf("expected duckdb backend, got %s", cfg.Storage.Backend)
	}
	if cfg.Logging.Level != "info" || cfg.Logging.Format != "json" {
		t.Fatalf("unexpected logging defaults: %+v", cfg.Logging)
	}
	if cfg.Matcher.Mode != "go" {
		t.Fatalf("expected go matcher by default, got %s", cfg.Matcher.Mode)
	}
	if cfg.Matcher.CacheSize != 1024 || cfg.Matcher.CacheTTL <= 0 {
		t.Fatalf("unexpected matcher defaults: %+v", cfg.Matcher)
	}
	if cfg.Collector.TailTransport != "http" {
		t.Fatalf("unexpected collector tail transport default: %+v", cfg.Collector)
	}
	if !cfg.Collector.TailEnabled || cfg.Collector.TailBufferSize != 2048 || cfg.Collector.TailBatchSize != 256 {
		t.Fatalf("unexpected collector tail defaults: %+v", cfg.Collector)
	}
	if cfg.Collector.TailFlushInterval != 500*time.Millisecond || cfg.Collector.TailReconnectBackoff != 2*time.Second {
		t.Fatalf("unexpected collector tail timing defaults: %+v", cfg.Collector)
	}
}

func TestDefaultAppliesEnvOverrides(t *testing.T) {
	t.Setenv("CORTEX_SERVER_HOST", "10.0.0.1")
	t.Setenv("CORTEX_SERVER_PORT", "8888")
	t.Setenv("CORTEX_DUCKDB_PATH", "/tmp/env.db")
	t.Setenv("CORTEX_POSTGRES_SSL_MODE", "disable")
	t.Setenv("CORTEX_LOG_LEVEL", "debug")
	t.Setenv("CORTEX_MATCHER_MODE", "rust")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	if cfg.Server.Host != "10.0.0.1" {
		t.Fatalf("expected env host override, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 8888 {
		t.Fatalf("expected env port override, got %d", cfg.Server.Port)
	}
	if cfg.Storage.DuckDB.Path != "/tmp/env.db" {
		t.Fatalf("expected env duckdb path override, got %s", cfg.Storage.DuckDB.Path)
	}
	if cfg.Storage.PostgreSQL.SSLMode != "disable" {
		t.Fatalf("expected env postgres SSL mode override, got %s", cfg.Storage.PostgreSQL.SSLMode)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("expected env log level override, got %s", cfg.Logging.Level)
	}
	if cfg.Matcher.Mode != "rust" {
		t.Fatalf("expected env matcher mode override, got %s", cfg.Matcher.Mode)
	}
}

func TestDefaultLoadsUserOverrideFile(t *testing.T) {
	dir := t.TempDir()
	// Write a user override file
	userYAML := []byte(`server:
  host: "192.168.1.100"
  port: 7777
storage:
  duckdb:
    path: /data/custom.db
`)
	path := filepath.Join(dir, "loza-cortex.yaml")
	if err := os.WriteFile(path, userYAML, 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	// Temporarily change cwd so findUserConfigFile finds our file
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origDir)

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	if cfg.Server.Host != "192.168.1.100" {
		t.Fatalf("expected user host override, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 7777 {
		t.Fatalf("expected user port override, got %d", cfg.Server.Port)
	}
	if cfg.Storage.DuckDB.Path != "/data/custom.db" {
		t.Fatalf("expected user duckdb path override, got %s", cfg.Storage.DuckDB.Path)
	}
}

func TestDefaultEnvOverridesUserFile(t *testing.T) {
	dir := t.TempDir()
	// User file sets port 7777
	userYAML := []byte(`server:
  port: 7777
`)
	path := filepath.Join(dir, "loza.yaml")
	if err := os.WriteFile(path, userYAML, 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origDir)

	// Env should win over user file
	t.Setenv("CORTEX_SERVER_PORT", "5555")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	if cfg.Server.Port != 5555 {
		t.Fatalf("expected env to override user file port, got %d", cfg.Server.Port)
	}
}

func TestDefaultRejectsMalformedUserOverrideFile(t *testing.T) {
	dir := t.TempDir()
	// A malformed override previously had its load error discarded, silently
	// dropping the whole file (including any auth/TLS settings it enabled).
	writeConfigFile(t, filepath.Join(dir, "loza-cortex.yaml"), "server: [")
	changeWorkingDirectory(t, dir)

	_, err := Default()
	if err == nil || !strings.Contains(err.Error(), "failed to parse runtime override") {
		t.Fatalf("Default error = %v, want runtime override parse error", err)
	}
}

func TestDefaultRejectsInvalidUserOverrideFields(t *testing.T) {
	dir := t.TempDir()
	// KnownFields rejects unknown keys; the old _ = loadFile(...) discarded
	// that error even though it can hide a misspelled security setting.
	writeConfigFile(t, filepath.Join(dir, "loza.yaml"), "server:\n  port: 7777\nnonexistent_setting: true\n")
	changeWorkingDirectory(t, dir)

	_, err := Default()
	if err == nil || !strings.Contains(err.Error(), "nonexistent_setting") {
		t.Fatalf("Default error = %v, want unknown-field parse error", err)
	}
}

func TestDefaultValidatesResultingConfig(t *testing.T) {
	dir := t.TempDir()
	// Enabling authentication without API keys must not yield a config that
	// callers can use; the old Default never called Validate.
	writeConfigFile(t, filepath.Join(dir, "loza-cortex.yaml"), "authentication:\n  enabled: true\n")
	changeWorkingDirectory(t, dir)

	_, err := Default()
	if err == nil || !strings.Contains(err.Error(), "at least one api key is required") {
		t.Fatalf("Default error = %v, want authentication validation error", err)
	}
}

func TestDefaultReturnsValidConfigWithoutOverride(t *testing.T) {
	dir := t.TempDir()
	changeWorkingDirectory(t, dir)

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default returned an invalid config: %v", err)
	}
	if cfg.Server.Port != 9312 || cfg.Storage.Backend != "duckdb" {
		t.Fatalf("unexpected in-memory defaults: port=%d backend=%s", cfg.Server.Port, cfg.Storage.Backend)
	}
}

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := []byte(`
server:
  host: 127.0.0.1
  port: 9312
storage:
  backend: duckdb
  duckdb:
    path: ./cortex.db
logging:
  level: info
  format: json
authentication:
  enabled: false
collector:
  url: http://localhost:9308
  source_of_truth: true
  query_table: events
  raw_column: raw
  timestamp_column: timestamp
`)
	if err := os.WriteFile(path, yaml, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("CORTEX_SERVER_HOST", "0.0.0.0")
	t.Setenv("CORTEX_SERVER_PORT", "9312")
	t.Setenv("CORTEX_DUCKDB_PATH", "./test.db")
	t.Setenv("CORTEX_LOG_LEVEL", "debug")
	t.Setenv("CORTEX_MATCHER_MODE", "rust")
	t.Setenv("CORTEX_MATCHER_PARALLELISM", "8")
	t.Setenv("CORTEX_MATCHER_CACHE_SIZE", "2048")
	t.Setenv("CORTEX_MATCHER_CACHE_TTL", "45s")
	t.Setenv("CORTEX_COLLECTOR_TAIL_ENABLED", "true")
	t.Setenv("CORTEX_COLLECTOR_TAIL_TRANSPORT", "websocket")
	t.Setenv("CORTEX_COLLECTOR_TAIL_BUFFER_SIZE", "4096")
	t.Setenv("CORTEX_COLLECTOR_TAIL_BATCH_SIZE", "128")
	t.Setenv("CORTEX_COLLECTOR_TAIL_FLUSH_INTERVAL", "750ms")
	t.Setenv("CORTEX_COLLECTOR_TAIL_RECONNECT_BACKOFF", "5s")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 9312 {
		t.Fatalf("env overrides not applied: %+v", cfg.Server)
	}
	if cfg.Storage.DuckDB.Path != "./test.db" {
		t.Fatalf("expected duckdb path override, got %s", cfg.Storage.DuckDB.Path)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("expected log level override, got %s", cfg.Logging.Level)
	}
	if cfg.Matcher.Mode != "rust" {
		t.Fatalf("expected matcher mode override, got %s", cfg.Matcher.Mode)
	}
	if cfg.Matcher.Parallelism != 8 || cfg.Matcher.CacheSize != 2048 || cfg.Matcher.CacheTTL != 45*time.Second {
		t.Fatalf("expected matcher overrides, got %+v", cfg.Matcher)
	}
	if cfg.Collector.TailTransport != "websocket" {
		t.Fatalf("expected collector tail transport override, got %+v", cfg.Collector)
	}
	if !cfg.Collector.TailEnabled || cfg.Collector.TailBufferSize != 4096 || cfg.Collector.TailBatchSize != 128 {
		t.Fatalf("expected collector tail overrides, got %+v", cfg.Collector)
	}
	if cfg.Collector.TailFlushInterval != 750*time.Millisecond || cfg.Collector.TailReconnectBackoff != 5*time.Second {
		t.Fatalf("expected collector tail timing overrides, got %+v", cfg.Collector)
	}
}

func TestApplyEnvOverridesRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "server port", key: "CORTEX_SERVER_PORT", val: "not-a-port"},
		{name: "postgres port", key: "CORTEX_POSTGRES_PORT", val: "5432x"},
		{name: "matcher parallelism", key: "CORTEX_MATCHER_PARALLELISM", val: "many"},
		{name: "matcher cache size", key: "CORTEX_MATCHER_CACHE_SIZE", val: "big"},
		{name: "matcher cache ttl", key: "CORTEX_MATCHER_CACHE_TTL", val: "45"},
		{name: "rate limit per api key rpm", key: "CORTEX_RATELIMIT_PER_API_KEY_RPM", val: "fast"},
		{name: "rate limit per ip rpm", key: "CORTEX_RATELIMIT_PER_IP_RPM", val: "fast"},
		{name: "collector timeout", key: "CORTEX_COLLECTOR_TIMEOUT", val: "soon"},
		{name: "collector max response bytes", key: "CORTEX_COLLECTOR_MAX_RESPONSE_BYTES", val: "huge"},
		{name: "collector tail buffer size", key: "CORTEX_COLLECTOR_TAIL_BUFFER_SIZE", val: "many"},
		{name: "collector tail batch size", key: "CORTEX_COLLECTOR_TAIL_BATCH_SIZE", val: "many"},
		{name: "collector tail flush interval", key: "CORTEX_COLLECTOR_TAIL_FLUSH_INTERVAL", val: "fast"},
		{name: "collector tail reconnect backoff", key: "CORTEX_COLLECTOR_TAIL_RECONNECT_BACKOFF", val: "fast"},
		{name: "auth enabled", key: "CORTEX_AUTH_ENABLED", val: "yep"},
		{name: "rate limit enabled", key: "CORTEX_RATELIMIT_ENABLED", val: "yep"},
		{name: "tls enabled", key: "CORTEX_TLS_ENABLED", val: "yep"},
		{name: "pii redaction enabled", key: "CORTEX_PII_REDACTION_ENABLED", val: "yep"},
		{name: "collector source of truth", key: "CORTEX_COLLECTOR_SOURCE_OF_TRUTH", val: "maybe"},
		{name: "collector tail enabled", key: "CORTEX_COLLECTOR_TAIL_ENABLED", val: "maybe"},
func TestParseEnvBool(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "true", value: "true", want: true},
		{name: "true with surrounding whitespace", value: "  true  ", want: true},
		{name: "uppercase", value: "TRUE", want: true},
		{name: "numeric one", value: "1", want: true},
		{name: "short t", value: "t", want: true},
		{name: "capitalized", value: "True", want: true},
		{name: "false", value: "false", want: false},
		{name: "false with whitespace", value: " false ", want: false},
		{name: "numeric zero", value: "0", want: false},
		{name: "short f", value: "f", want: false},
		{name: "yes is invalid", value: "yes", wantErr: true},
		{name: "typo is invalid", value: "tru", wantErr: true},
		{name: "empty is invalid", value: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			cfg := defaultConfig()
			err := applyEnvOverrides(cfg)
			if err == nil {
				t.Fatalf("applyEnvOverrides accepted invalid %s=%q", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error %q does not identify %s", err, tc.key)
			}
			if !strings.Contains(err.Error(), tc.val) {
				t.Fatalf("error %q does not include rejected value %q", err, tc.val)
			}
		})
	}
}

func TestApplyEnvOverridesAcceptsValidValues(t *testing.T) {
	t.Setenv("CORTEX_SERVER_PORT", "8080")
	t.Setenv("CORTEX_MATCHER_CACHE_TTL", "45s")
	t.Setenv("CORTEX_COLLECTOR_MAX_RESPONSE_BYTES", "16777216")
	t.Setenv("CORTEX_TLS_ENABLED", "TRUE")
	t.Setenv("CORTEX_COLLECTOR_TAIL_ENABLED", "1")

	cfg := defaultConfig()
	if err := applyEnvOverrides(cfg); err != nil {
		t.Fatalf("applyEnvOverrides: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Matcher.CacheTTL != 45*time.Second || cfg.Collector.MaxResponseBytes != 16777216 {
		t.Fatalf("numeric/duration overrides not applied: %+v %+v", cfg.Server, cfg.Matcher)
	}
	if !cfg.TLS.Enabled || !cfg.Collector.TailEnabled {
		t.Fatalf("boolean overrides not applied: %+v %+v", cfg.TLS, cfg.Collector)
	}
}

func TestLoadRejectsInvalidEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfigFile(t, path, `server:
  port: 9312
storage:
  backend: duckdb
  duckdb:
    path: ./cortex.db
logging:
  level: info
  format: json
`)

	t.Setenv("CORTEX_SERVER_PORT", "not-a-port")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "CORTEX_SERVER_PORT") {
		t.Fatalf("Load error = %v, want invalid CORTEX_SERVER_PORT error", err)
	}
}

func TestLoadDefaultRejectsInvalidEnvironmentOverride(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)
	t.Setenv("CORTEX_MATCHER_CACHE_TTL", "not-a-duration")

	_, err := LoadDefault()
	if err == nil || !strings.Contains(err.Error(), "CORTEX_MATCHER_CACHE_TTL") {
		t.Fatalf("LoadDefault error = %v, want invalid CORTEX_MATCHER_CACHE_TTL error", err)
	}
}

func TestDefaultRejectsInvalidEnvironmentOverride(t *testing.T) {
	t.Setenv("CORTEX_COLLECTOR_MAX_RESPONSE_BYTES", "not-an-int")

	cfg, err := Default()
	if err == nil {
		t.Fatal("Default accepted invalid environment override")
	}
	if cfg != nil {
		t.Fatalf("Default returned a config on error: %+v", cfg)
	}
	if !strings.Contains(err.Error(), "CORTEX_COLLECTOR_MAX_RESPONSE_BYTES") {
		t.Fatalf("Default error = %v, want invalid CORTEX_COLLECTOR_MAX_RESPONSE_BYTES error", err)
			got, err := parseEnvBool("CORTEX_AUTH_ENABLED", tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseEnvBool(%q) expected an error", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEnvBool(%q): %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("parseEnvBool(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestApplyEnvOverridesBooleanToggles(t *testing.T) {
	// Values the old `== "true"` / `EqualFold(v, "true")` comparisons rejected
	// even though strconv.ParseBool considers them valid.
	values := map[string]string{
		"CORTEX_AUTH_ENABLED":           " true ",
		"CORTEX_RATELIMIT_ENABLED":      "1",
		"CORTEX_TLS_ENABLED":            "TRUE",
		"CORTEX_PII_REDACTION_ENABLED":  "t",
		"CORTEX_COLLECTOR_TAIL_ENABLED": "True",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}

	cfg := &Config{}
	if err := applyEnvOverrides(cfg); err != nil {
		t.Fatalf("applyEnvOverrides: %v", err)
	}

	if !cfg.Authentication.Enabled {
		t.Error("CORTEX_AUTH_ENABLED=\" true \" should enable authentication")
	}
	if !cfg.RateLimit.Enabled {
		t.Error("CORTEX_RATELIMIT_ENABLED=1 should enable rate limiting")
	}
	if !cfg.TLS.Enabled {
		t.Error("CORTEX_TLS_ENABLED=TRUE should enable TLS")
	}
	if !cfg.PIIRedaction.Enabled {
		t.Error("CORTEX_PII_REDACTION_ENABLED=t should enable PII redaction")
	}
	if !cfg.Collector.TailEnabled {
		t.Error("CORTEX_COLLECTOR_TAIL_ENABLED=True should enable collector tail")
	}
}

func TestApplyEnvOverridesRejectsInvalidBooleanToggle(t *testing.T) {
	toggles := []string{
		"CORTEX_AUTH_ENABLED",
		"CORTEX_RATELIMIT_ENABLED",
		"CORTEX_TLS_ENABLED",
		"CORTEX_PII_REDACTION_ENABLED",
		"CORTEX_COLLECTOR_TAIL_ENABLED",
	}

	for _, bad := range toggles {
		t.Run(bad, func(t *testing.T) {
			// Keep every other toggle valid so the failure is attributable to
			// the toggle under test.
			for _, name := range toggles {
				value := "true"
				if name == bad {
					value = "yes" // not accepted by strconv.ParseBool
				}
				t.Setenv(name, value)
			}

			cfg := &Config{}
			err := applyEnvOverrides(cfg)
			if err == nil {
				t.Fatalf("applyEnvOverrides accepted invalid %s=%q", bad, "yes")
			}
			if !strings.Contains(err.Error(), bad) {
				t.Fatalf("error %q does not identify %s", err, bad)
			}
		})
	}
}

func TestLoadRejectsInvalidBooleanOverride(t *testing.T) {
	t.Setenv("CORTEX_AUTH_ENABLED", "yes")

	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFile(t, path, "server:\n  port: 9312\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "CORTEX_AUTH_ENABLED") {
		t.Fatalf("Load error = %v, want error mentioning CORTEX_AUTH_ENABLED", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid duckdb",
			cfg: Config{
				Server: ServerConfig{Port: 9312},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "go", CacheSize: 128},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
		},
		{
			name: "invalid port",
			cfg: Config{
				Server: ServerConfig{Port: 0},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "go"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
		},
		{
			name: "missing duckdb path",
			cfg: Config{
				Server:  ServerConfig{Port: 9312},
				Storage: StorageConfig{Backend: "duckdb"},
				Matcher: MatcherConfig{Mode: "go"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
		},
		{
			name: "invalid matcher mode",
			cfg: Config{
				Server: ServerConfig{Port: 9312},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "wasm"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
		},
		{
			name: "negative matcher cache size",
			cfg: Config{
				Server: ServerConfig{Port: 9312},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "go", CacheSize: -1},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
		},
		{
			name: "negative collector tail buffer",
			cfg: Config{
				Server: ServerConfig{Port: 9312},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "go"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				Collector: CollectorConfig{
					SourceOfTruth:   true,
					URL:             "http://localhost:9308",
					QueryTable:      "events",
					RawColumn:       "raw",
					TimestampColumn: "timestamp",
					TailBufferSize:  -1,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid collector tail transport",
			cfg: Config{
				Server: ServerConfig{Port: 9312},
				Storage: StorageConfig{
					Backend: "duckdb",
					DuckDB:  DuckDBConfig{Path: "./cortex.db"},
				},
				Matcher: MatcherConfig{Mode: "go"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				Collector: CollectorConfig{
					SourceOfTruth:   true,
					URL:             "http://localhost:9308",
					QueryTable:      "events",
					RawColumn:       "raw",
					TimestampColumn: "timestamp",
					TailTransport:   "grpc",
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultPostgresSSLModeIsSecure(t *testing.T) {
	if got := defaultConfig().Storage.PostgreSQL.SSLMode; got != "require" {
		t.Fatalf("default postgres ssl_mode = %q, want %q", got, "require")
	}
}

func TestValidatePostgresSSLMode(t *testing.T) {
	newPostgresConfig := func(host, sslMode string) Config {
		cfg := *defaultConfig()
		cfg.Storage.Backend = "postgres"
		cfg.Storage.PostgreSQL.Host = host
		cfg.Storage.PostgreSQL.SSLMode = sslMode
		return cfg
	}

	tests := []struct {
		name     string
		host     string
		mode     string
		wantErr  bool
		wantMode string
	}{
		{name: "require allowed for remote host", host: "db.example.com", mode: "require", wantMode: "require"},
		{name: "verify-full allowed for remote host", host: "db.example.com", mode: "verify-full", wantMode: "verify-full"},
		{name: "verify-ca allowed for remote host", host: "db.example.com", mode: "verify-ca", wantMode: "verify-ca"},
		{name: "disable rejected for remote host", host: "db.example.com", mode: "disable", wantErr: true},
		{name: "allow rejected for remote host", host: "db.example.com", mode: "allow", wantErr: true},
		{name: "prefer rejected for remote host", host: "db.example.com", mode: "prefer", wantErr: true},
		{name: "disable rejected for remote IP", host: "10.0.0.5", mode: "disable", wantErr: true},
		{name: "disable allowed for localhost", host: "localhost", mode: "disable", wantMode: "disable"},
		{name: "disable allowed for loopback IP", host: "127.0.0.1", mode: "disable", wantMode: "disable"},
		{name: "disable allowed for local container service", host: "postgres", mode: "disable", wantMode: "disable"},
		{name: "unknown mode rejected", host: "localhost", mode: "bogus", wantErr: true},
		{name: "empty mode normalized to require", host: "db.example.com", mode: "", wantMode: "require"},
		{name: "mode normalized to lowercase", host: "db.example.com", mode: "Verify-Full", wantMode: "verify-full"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newPostgresConfig(tc.host, tc.mode)
			err := cfg.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if cfg.Storage.PostgreSQL.SSLMode != tc.wantMode {
				t.Fatalf("ssl_mode = %q, want %q", cfg.Storage.PostgreSQL.SSLMode, tc.wantMode)
			}
		})
	}
}

func TestLoadDefaultPipeline(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)

	dir := t.TempDir()
	writeConfigFile(t, filepath.Join(dir, "loza-cortex.yaml"), "server:\n  port: 7777\n")
	changeWorkingDirectory(t, dir)
	t.Setenv("CORTEX_SERVER_PORT", "8888")

	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg.Server.Port != 8888 {
		t.Fatalf("environment must override cwd config, got port %d", cfg.Server.Port)
	}
	if cfg.Storage.DuckDB.Path != "./bundled.db" {
		t.Fatalf("expected bundled storage configuration, got %q", cfg.Storage.DuckDB.Path)
	}
}

func TestLoadDefaultSkipsProjectManifest(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)

	dir := t.TempDir()
	writeConfigFile(t, filepath.Join(dir, "loza.yaml"), "name: loza\nkind: release\nmodule: github.com/astraive/loza\n")
	changeWorkingDirectory(t, dir)

	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg.Server.Port != 9312 {
		t.Fatalf("release manifest must not override bundled config, got port %d", cfg.Server.Port)
	}
}

func TestLoadDefaultRejectsInvalidRuntimeOverride(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "malformed YAML", content: "server: [", want: "failed to parse runtime override"},
		{name: "invalid configuration", content: "server:\n  port: 0\n", want: "invalid configuration"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defaults := writeBundledDefaults(t)
			useBundledDefaults(t, defaults)
			dir := t.TempDir()
			writeConfigFile(t, filepath.Join(dir, "loza.yaml"), tc.content)
			changeWorkingDirectory(t, dir)

			_, err := LoadDefault()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadDefault error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadOverlaysBundledDefaultsThenEnvironment(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)

	override := filepath.Join(t.TempDir(), "runtime.yaml")
	writeConfigFile(t, override, "server:\n  port: 7777\n")
	t.Setenv("CORTEX_SERVER_PORT", "8888")

	cfg, err := Load(override)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8888 {
		t.Fatalf("environment must override named config, got port %d", cfg.Server.Port)
	}
	if cfg.Storage.DuckDB.Path != "./bundled.db" {
		t.Fatalf("named config must retain bundled fields, got %q", cfg.Storage.DuckDB.Path)
	}
}

func TestValidateEnabledAuthenticationRequiresValidKey(t *testing.T) {
	base := func() Config {
		return Config{
			Server:  ServerConfig{Port: 9312},
			Storage: StorageConfig{Backend: "duckdb", DuckDB: DuckDBConfig{Path: "./cortex.db"}},
			Matcher: MatcherConfig{Mode: "go"},
			Logging: LoggingConfig{Level: "info", Format: "json"},
			Authentication: AuthenticationConfig{
				Enabled: true,
			},
		}
	}

	tests := []struct {
		name string
		key  []APIKey
		ok   bool
	}{
		{name: "no keys"},
		{name: "empty name", key: []APIKey{{Key: "secret", Role: "reader"}}},
		{name: "empty key", key: []APIKey{{Name: "reader", Role: "reader"}}},
		{name: "invalid role", key: []APIKey{{Name: "service", Key: "secret", Role: "operator"}}},
		{name: "valid reader", key: []APIKey{{Name: "reader", Key: "secret", Role: "reader"}}, ok: true},
		{name: "valid writer", key: []APIKey{{Name: "writer", Key: "secret", Role: "writer"}}, ok: true},
		{name: "valid admin", key: []APIKey{{Name: "admin", Key: "secret", Role: "admin"}}, ok: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			cfg.Authentication.APIKeys = tc.key
			err := cfg.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []APIKey
		wantErr bool
	}{
		{
			name: "single valid key",
			raw:  "my-service:sk_abc123:writer",
			want: []APIKey{{Name: "my-service", Key: "sk_abc123", Role: "writer"}},
		},
		{
			name: "multiple valid keys with whitespace",
			raw:  "my-service:sk_abc123:writer, read-only:sk_def456:reader ",
			want: []APIKey{
				{Name: "my-service", Key: "sk_abc123", Role: "writer"},
				{Name: "read-only", Key: "sk_def456", Role: "reader"},
			},
		},
		{
			name: "empty entries and trailing comma ignored",
			raw:  "my-service:sk_abc123:writer,,",
			want: []APIKey{{Name: "my-service", Key: "sk_abc123", Role: "writer"}},
		},
		{
			// A colon in the key used to be SplitN'd into the role, corrupting it.
			name:    "key containing colon is rejected",
			raw:     "my-service:sk:abc:writer",
			wantErr: true,
		},
		{
			name:    "too few fields",
			raw:     "my-service:sk_abc123",
			wantErr: true,
		},
		{
			name:    "empty name",
			raw:     ":sk_abc123:writer",
			wantErr: true,
		},
		{
			name:    "empty key",
			raw:     "my-service::writer",
			wantErr: true,
		},
		{
			name:    "empty role",
			raw:     "my-service:sk_abc123:",
			wantErr: true,
		},
		{
			name:    "one malformed entry among valid ones",
			raw:     "my-service:sk_abc123:writer,bad:key:with:colon,read-only:sk_def456:reader",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAPIKeys(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error for malformed CORTEX_API_KEYS")
				}
				if !strings.Contains(err.Error(), "CORTEX_API_KEYS") {
					t.Fatalf("error should identify the malformed variable, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAPIKeys: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseAPIKeys = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoadRejectsMalformedAPIKeys(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)

	override := filepath.Join(t.TempDir(), "runtime.yaml")
	writeConfigFile(t, override, "authentication:\n  enabled: true\n")

	// Previously this entry was silently misparsed: SplitN folded the extra
	// colon into the role, and a bad role was only caught if auth was enabled.
	t.Setenv("CORTEX_API_KEYS", "my-service:sk:with:colons:admin")

	if _, err := Load(override); err == nil {
		t.Fatal("expected Load to reject malformed CORTEX_API_KEYS")
	} else if !strings.Contains(err.Error(), "CORTEX_API_KEYS") {
		t.Fatalf("error should identify the malformed variable, got: %v", err)
	}
}

func TestLoadAppliesValidAPIKeysFromEnv(t *testing.T) {
	defaults := writeBundledDefaults(t)
	useBundledDefaults(t, defaults)

	override := filepath.Join(t.TempDir(), "runtime.yaml")
	writeConfigFile(t, override, "authentication:\n  enabled: true\n")

	t.Setenv("CORTEX_API_KEYS", "my-service:sk_abc123:writer")

	cfg, err := Load(override)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []APIKey{{Name: "my-service", Key: "sk_abc123", Role: "writer"}}
	if !reflect.DeepEqual(cfg.Authentication.APIKeys, want) {
		t.Fatalf("API keys = %+v, want %+v", cfg.Authentication.APIKeys, want)
	}
}

func writeBundledDefaults(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), bundledDefaultFilename)
	writeConfigFile(t, path, `server:
  port: 9312
storage:
  backend: duckdb
  duckdb:
    path: ./bundled.db
matcher:
  mode: go
logging:
  level: info
  format: json
authentication:
  enabled: false
`)
	return path
}

func useBundledDefaults(t *testing.T, path string) {
	t.Helper()
	previous := bundledDefaultCandidates
	bundledDefaultCandidates = func() []string { return []string{path} }
	t.Cleanup(func() { bundledDefaultCandidates = previous })
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

func changeWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
