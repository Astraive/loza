package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

// writeReloadConfig writes the scoped-auth config used by the reload tests.
// The extra YAML fragment lets tests mutate a single non-auth setting.
func writeReloadConfig(t *testing.T, path string, authEnabled bool, extra string) {
	t.Helper()
	raw := fmt.Sprintf(`
auth:
  enabled: %t
  server_secret: ${COLLECTOR_AUTH_SERVER_SECRET}
  cache_ttl: 1m
  negative_cache_ttl: 10s
  collectors:
    - slug: orders
    - slug: billing
  keys:
    - name: orders-writer
      key_id: korders
      secret_env: COLLECTOR_INGEST_KEY_SECRET
      kind: sec
      collector: orders
      permissions: [events:write]
      allowed_envs: [production]
storage:
  encryption_key_env: LOZA_STORAGE_ENCRYPTION_KEY
`, authEnabled) + extra
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write reload config: %v", err)
	}
}

func loadReloadConfig(t *testing.T, path string) *collectorState {
	t.Helper()
	cfg, err := loadCollectorConfigFromArgs([]string{"-c", path})
	if err != nil {
		t.Fatalf("load reload config: %v", err)
	}
	state := &collectorState{
		cfg:         cfg,
		ingestSink:  &fakeSink{},
		rateLimiter: rate.NewLimiter(rate.Limit(1000), 1000),
	}
	state.ready.Store(true)
	return state
}

// TestHotReloadCannotDisableCollectorAuthorization reproduces the finding
// that a SIGHUP reload changing auth.enabled silently removed the per-collector
// authorization guard while the startup authentication middleware stayed in
// place. The reload must be rejected and the guard must remain enforced.
func TestHotReloadCannotDisableCollectorAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.yaml")
	writeReloadConfig(t, path, true, "")
	state := loadReloadConfig(t, path)

	srv := httptest.NewServer(buildMux(state))
	defer srv.Close()

	crossCollectorStatus := func() int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/collectors/billing/events", strings.NewReader(`{"event":"cross"}`))
		if err != nil {
			t.Fatal(err)
		}
		// Credential is scoped to "orders", so writing to "billing" must be denied.
		req.Header.Set("Authorization", "Bearer lz_sec_live_korders_test-ingest-key-secret")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Loza-Env", "production")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := crossCollectorStatus(); got != http.StatusForbidden {
		t.Fatalf("baseline cross-collector status = %d, want 403", got)
	}

	// An operator disables auth in the config file and sends SIGHUP.
	writeReloadConfig(t, path, false, "")
	if err := reloadCollectorState(state); err == nil {
		t.Fatal("expected the auth-changing hot reload to be rejected")
	}
	if !state.cfg.authEnabled {
		t.Fatal("hot reload changed auth.enabled")
	}
	if got := crossCollectorStatus(); got != http.StatusForbidden {
		t.Fatalf("cross-collector status after rejected reload = %d, want 403 (authorization must stay enforced)", got)
	}
}

// TestHotReloadAllowsNonAuthConfigChange keeps the fix scoped: a reload that
// does not touch the auth policy must still apply mutable runtime settings.
func TestHotReloadAllowsNonAuthConfigChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.yaml")
	writeReloadConfig(t, path, true, "")
	state := loadReloadConfig(t, path)

	writeReloadConfig(t, path, true, "rate_limit:\n  enabled: true\n  rps: 25\n  burst: 50\n")
	if err := reloadCollectorState(state); err != nil {
		t.Fatalf("non-auth reload was rejected: %v", err)
	}
	if !state.cfg.authEnabled {
		t.Fatal("auth.enabled changed on a non-auth reload")
	}
	if !state.cfg.rateLimitEnabled || state.cfg.rateLimitRPS != 25 || state.cfg.rateLimitBurst != 50 {
		t.Fatalf("mutable rate-limit policy was not applied: enabled=%v rps=%v burst=%v",
			state.cfg.rateLimitEnabled, state.cfg.rateLimitRPS, state.cfg.rateLimitBurst)
	}
}

func TestStartupAuthPolicyDetectsEveryAuthField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.yaml")
	writeReloadConfig(t, path, true, "")
	base := loadReloadConfig(t, path)
	before := startupAuthPolicy(base.cfg)

	mutations := map[string]func(*collectorConfig){
		"enabled":           func(c *collectorConfig) { c.authEnabled = !c.authEnabled },
		"server_secret":     func(c *collectorConfig) { c.authServerSecret = "different" },
		"default_collector": func(c *collectorConfig) { c.authDefaultCollector = "other" },
		"api_key":           func(c *collectorConfig) { c.apiKey = "different" },
		"trusted_proxies":   func(c *collectorConfig) { c.trustedProxies = []string{"10.0.0.0/8"} },
		"identity_mode":     func(c *collectorConfig) { c.identityMode = "jwt" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			next := base.cfg
			mutate(&next)
			if reflect.DeepEqual(startupAuthPolicy(next), before) {
				t.Fatalf("auth policy change in %q was not detected", name)
			}
		})
	}
}
