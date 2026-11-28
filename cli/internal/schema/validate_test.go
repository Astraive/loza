package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	speccontract "github.com/astraive/loza/spec/generated/go/contract"
)

type conformanceManifest struct {
	Valid             []string `json:"valid"`
	LooseOnlyValid    []string `json:"loose_only_valid"`
	Invalid           []string `json:"invalid"`
	StrictOnlyInvalid []string `json:"strict_only_invalid"`
	InvalidIngest     []string `json:"invalid_ingest"`
	InvalidCollector  []string `json:"invalid_collector_response"`
	InvalidLimits     []string `json:"invalid_limits"`
	ManifestDirectory string
}

func TestValidateSpecAssets(t *testing.T) {
	if err := ValidateSpecAssets(specRepoRoot(t)); err != nil {
		t.Fatalf("ValidateSpecAssets failed: %v", err)
	}
}

func TestValidateGoldenFixtures(t *testing.T) {
	root := specRepoRoot(t)
	manifest := loadConformanceManifest(t, root)

	validPaths := resolveFixturePaths(t, manifest.ManifestDirectory, manifest.Valid)
	if len(validPaths) == 0 {
		t.Fatal("expected valid fixtures")
	}
	for _, path := range validPaths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			if err := ValidateEventFile(path); err != nil {
				t.Fatalf("expected valid fixture to pass, got %v", err)
			}
		})
	}

	invalidPaths := resolveFixturePaths(t, manifest.ManifestDirectory,
		append(append(append(append(manifest.Invalid, manifest.StrictOnlyInvalid...), manifest.InvalidIngest...), manifest.InvalidCollector...), manifest.InvalidLimits...),
	)
	if len(invalidPaths) == 0 {
		t.Fatal("expected invalid fixtures")
	}
	for _, path := range invalidPaths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			if err := ValidateEventFile(path); err == nil {
				t.Fatal("expected invalid fixture to fail")
			}
		})
	}

	for _, path := range resolveFixturePaths(t, manifest.ManifestDirectory, manifest.LooseOnlyValid) {
		path := path
		t.Run("strict_rejects_"+filepath.Base(path), func(t *testing.T) {
			if err := ValidateEventFile(path); err == nil {
				t.Fatal("expected loose-only valid fixture to fail strict validation")
			}
		})
	}

	// The invalid_limits fixtures are now validated alongside the other invalid
	// cases above; keep an explicit presence guard so an empty manifest list is
	// still reported instead of silently passing.
	if len(resolveFixturePaths(t, manifest.ManifestDirectory, manifest.InvalidLimits)) == 0 {
		t.Fatal("expected invalid_limits fixtures")
	}
}

// TestValidateOversizedPayload is a focused regression test for max-size
// enforcement. It guards against the same blind spot that let the
// invalid_limits fixtures go unvalidated: a payload exceeding MaxEventBytes
// must be rejected, while an otherwise identical in-limit event must pass.
func TestValidateOversizedPayload(t *testing.T) {
	inLimit := []byte(`{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}`)
	if err := ValidateEventBytes(inLimit); err != nil {
		t.Fatalf("in-limit payload should pass: %v", err)
	}

	oversized := []byte(`{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_oversized","service":"checkout","event":"checkout.request","kind":"http","message":"` +
		strings.Repeat("x", speccontract.MaxEventBytes) + `"}`)
	if len(oversized) <= speccontract.MaxEventBytes {
		t.Fatalf("test setup: payload size %d must exceed MaxEventBytes %d", len(oversized), speccontract.MaxEventBytes)
	}
	if err := ValidateEventBytes(oversized); err == nil {
		t.Fatal("expected oversized payload to be rejected by max_event_size_bytes")
	}
}

func TestValidateWrappedAndNDJSON(t *testing.T) {
	wrapped := []byte(`{"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}]}`)
	if err := ValidateEventBytes(wrapped); err != nil {
		t.Fatalf("wrapped payload should pass: %v", err)
	}

	ndjson := []byte("{\"schema_version\":\"v1\",\"event_version\":\"v1\",\"timestamp\":\"2026-05-12T00:00:00Z\",\"event_id\":\"evt_1\",\"service\":\"checkout\",\"event\":\"checkout.request\",\"kind\":\"http\"}\n{\"schema_version\":\"v1\",\"event_version\":\"v1\",\"timestamp\":\"2026-05-12T00:01:00Z\",\"event_id\":\"evt_2\",\"service\":\"checkout\",\"event\":\"checkout.request\",\"kind\":\"http\"}\n")
	if err := ValidateEventBytes(ndjson); err != nil {
		t.Fatalf("ndjson payload should pass: %v", err)
	}
}

func TestValidateWrappedEventsWithNullAPIVersion(t *testing.T) {
	// "api_version": null unmarshals to a nil interface value, which must not be
	// confused with an absent api_version key. An explicit null is a present key
	// and must be routed through full ingest-envelope validation, which requires
	// a non-empty string api_version.
	nullVersion := []byte(`{"api_version":null,"source":{"sdk":"loza-cli","service":"checkout"},"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http"}]}`)

	if err := speccontract.ValidateFlexibleJSONBytes(nullVersion, true); err == nil {
		t.Fatal("spec contract should reject an explicit null api_version")
	}
	if err := ValidateEventBytes(nullVersion); err == nil {
		t.Fatal("ValidateEventBytes should reject an explicit null api_version instead of taking the wrapped-events shortcut")
	}
}

// TestValidateOversizedPayloadsRejected covers the size-limit bypass where a
// payload containing an "events" key or an embedded newline skipped the limit.
// The global bound is now shape-independent, and per-event limits are applied
// after parsing.
func TestValidateOversizedPayloadsRejected(t *testing.T) {
	oversizedPadding := strings.Repeat("x", speccontract.MaxEventBytes+1024)

	cases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "oversized event object with events-like key",
			raw:  []byte(fmt.Sprintf(`{"events":%q}`, oversizedPadding)),
		},
		{
			name: "oversized payload with embedded newline",
			raw:  []byte("{\n\"data\":\"" + oversizedPadding + "\"\n}"),
		},
		{
			name: "oversized wrapped events envelope",
			raw: []byte(fmt.Sprintf(
				`{"api_version":"v1","source":{"sdk":"test","version":"v1","service":"svc"},"events":[{"schema_version":"v1","event_version":"v1","timestamp":"2026-05-12T00:00:00Z","event_id":"evt_1","service":"checkout","event":"checkout.request","kind":"http","data":%q}]}`,
				oversizedPadding,
			)),
		},
		{
			name: "oversized raw payload",
			raw:  []byte(oversizedPadding),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(bytes.TrimSpace(tc.raw)) <= speccontract.MaxEventBytes {
				t.Fatalf("test payload must exceed MaxEventBytes (%d)", speccontract.MaxEventBytes)
			}
			if err := ValidateEventBytes(tc.raw); err == nil {
				t.Fatal("ValidateEventBytes should reject oversized payload")
			}
		})
	}
}

func specRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "spec"))
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("spec repo path not found: %v", err)
	}
	return root
}

func loadConformanceManifest(t *testing.T, root string) conformanceManifest {
	t.Helper()
	manifestPath := filepath.Join(root, "conformance", "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		manifestPath = filepath.Join(root, "examples", "golden", "manifest.json")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read conformance manifest: %v", err)
	}
	var manifest conformanceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode conformance manifest: %v", err)
	}
	manifest.ManifestDirectory = filepath.Dir(manifestPath)
	return manifest
}

func resolveFixturePaths(t *testing.T, manifestDir string, rels []string) []string {
	t.Helper()
	paths := make([]string, 0, len(rels))
	for _, rel := range rels {
		path := filepath.Clean(filepath.Join(manifestDir, rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fixture path not found: %s", path)
		}
		paths = append(paths, path)
	}
	return paths
}
