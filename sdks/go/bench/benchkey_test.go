package bench

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

const (
	// benchAPIKeyEnv optionally supplies an API key for the benchmark suite.
	// It is never required: benchmarks fall back to a clearly non-credential
	// placeholder so no live-key-shaped secret is committed to source.
	benchAPIKeyEnv = "LOZA_BENCH_API_KEY"

	// benchAPIKeyPlaceholder is intentionally not a credential: it carries no
	// production key prefix and exists only so the auth config path is exercised.
	benchAPIKeyPlaceholder = "bench-placeholder-api-key"
)

// benchAPIKey returns the API key used by the auth benchmarks. A value from the
// environment takes precedence; otherwise a non-credential placeholder is used.
// The benchmarks emit to an in-memory sink and never authenticate against a real
// collector, so the key only needs to be present in the config for the auth code
// path to be measured.
func benchAPIKey() string {
	if key := strings.TrimSpace(os.Getenv(benchAPIKeyEnv)); key != "" {
		return key
	}
	return benchAPIKeyPlaceholder
}

// liveKeyPrefix is assembled at runtime so these test sources never embed a
// live-key-shaped credential literal themselves.
func liveKeyPrefix() string { return "lz_sec" + "_live_" }

// TestBenchAPIKeyHasNoLiveKeyPrefix guards against reintroducing a hardcoded,
// production-key-shaped credential into the benchmark suite.
func TestBenchAPIKeyHasNoLiveKeyPrefix(t *testing.T) {
	t.Setenv(benchAPIKeyEnv, "")
	key := benchAPIKey()
	if key == "" {
		t.Fatal("benchAPIKey() returned an empty key")
	}
	if strings.HasPrefix(key, liveKeyPrefix()) {
		t.Fatalf("benchAPIKey() uses a live-key prefix: %q", key)
	}
	if key != benchAPIKeyPlaceholder {
		t.Fatalf("benchAPIKey() = %q, want placeholder %q", key, benchAPIKeyPlaceholder)
	}
}

// TestBenchAPIKeyFromEnv verifies the optional environment override.
func TestBenchAPIKeyFromEnv(t *testing.T) {
	t.Setenv(benchAPIKeyEnv, "  custom-bench-key  ")
	if got := benchAPIKey(); got != "custom-bench-key" {
		t.Fatalf("benchAPIKey() = %q, want %q", got, "custom-bench-key")
	}
}

// TestBenchSourcesHaveNoHardcodedLiveKey scans the benchmark sources to ensure
// no production-key-shaped credential literal is committed.
func TestBenchSourcesHaveNoHardcodedLiveKey(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read bench dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if bytes.Contains(src, []byte(liveKeyPrefix())) {
			t.Errorf("%s contains a hardcoded live-key prefix", e.Name())
		}
	}
}
