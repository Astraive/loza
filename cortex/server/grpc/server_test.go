package grpcserver

import (
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/redaction"
)

func TestBuildRedactConfig_DefaultEnforceWithBlocklist(t *testing.T) {
	cfg := &config.Config{
		PIIRedaction: config.PIIRedactionConfig{
			Enabled:   true,
			Mode:      "",
			Blocklist: []string{"customer_ssn"},
		},
	}

	got := buildRedactConfig(cfg)
	if got.Mode != redaction.ModeEnforce {
		t.Fatalf("Mode = %q, want %q when redaction is enabled with a blocklist and no explicit mode", got.Mode, redaction.ModeEnforce)
	}
	if len(got.Blocklist) != 1 || got.Blocklist[0] != "customer_ssn" {
		t.Fatalf("Blocklist = %v, want [customer_ssn]", got.Blocklist)
	}
}

func TestBuildRedactConfig_ExplicitModePreserved(t *testing.T) {
	cfg := &config.Config{
		PIIRedaction: config.PIIRedactionConfig{
			Enabled:   true,
			Mode:      string(redaction.ModeLog),
			Blocklist: []string{"customer_ssn"},
		},
	}

	got := buildRedactConfig(cfg)
	if got.Mode != redaction.ModeLog {
		t.Fatalf("Mode = %q, want %q when an explicit mode is configured", got.Mode, redaction.ModeLog)
	}
}

func TestBuildRedactConfig_DisabledLeavesModeUnset(t *testing.T) {
	cfg := &config.Config{
		PIIRedaction: config.PIIRedactionConfig{
			Enabled:   false,
			Blocklist: []string{"customer_ssn"},
		},
	}

	got := buildRedactConfig(cfg)
	if got.Mode != "" {
		t.Fatalf("Mode = %q, want empty when redaction is disabled", got.Mode)
	}
}

func TestBuildRedactConfig_DefaultEnforceWithoutBlocklist(t *testing.T) {
	cfg := &config.Config{
		PIIRedaction: config.PIIRedactionConfig{
			Enabled: true,
		},
	}

	got := buildRedactConfig(cfg)
	if got.Mode != redaction.ModeEnforce {
		t.Fatalf("Mode = %q, want %q when redaction is enabled with no blocklist", got.Mode, redaction.ModeEnforce)
	}
	if len(got.Allowlist) == 0 {
		t.Fatal("Allowlist should be populated with defaults when redaction is enabled")
	}
}
