package core

import "testing"

// TestMergeCodeConfig_CollectorURLWinsOverDeprecatedEndpoint verifies that the
// deprecated CollectorEndpoint does not override the supported CollectorURL
// when both are supplied at the code level (issue #261).
func TestMergeCodeConfig_CollectorURLWinsOverDeprecatedEndpoint(t *testing.T) {
	base := Config{CollectorURL: "http://base-collector:9308"}
	code := Config{
		CollectorURL:      "http://supported-collector:9308",
		CollectorEndpoint: "http://deprecated-endpoint:9308",
	}

	merged := mergeCodeConfig(base, code)

	if merged.CollectorURL != "http://supported-collector:9308" {
		t.Errorf("CollectorURL = %q, want supported CollectorURL %q", merged.CollectorURL, "http://supported-collector:9308")
	}
	if merged.CollectorEndpoint != "http://deprecated-endpoint:9308" {
		t.Errorf("CollectorEndpoint = %q, want deprecated endpoint value %q", merged.CollectorEndpoint, "http://deprecated-endpoint:9308")
	}
}

// TestMergeCodeConfig_DeprecatedEndpointFallback verifies that CollectorEndpoint
// is still honored as a fallback when CollectorURL is not supplied in code.
func TestMergeCodeConfig_DeprecatedEndpointFallback(t *testing.T) {
	base := Config{CollectorURL: "http://base-collector:9308"}
	code := Config{CollectorEndpoint: "http://deprecated-endpoint:9308"}

	merged := mergeCodeConfig(base, code)

	if merged.CollectorURL != "http://deprecated-endpoint:9308" {
		t.Errorf("CollectorURL = %q, want deprecated endpoint fallback %q", merged.CollectorURL, "http://deprecated-endpoint:9308")
	}
	if merged.CollectorEndpoint != "http://deprecated-endpoint:9308" {
		t.Errorf("CollectorEndpoint = %q, want %q", merged.CollectorEndpoint, "http://deprecated-endpoint:9308")
	}
}
