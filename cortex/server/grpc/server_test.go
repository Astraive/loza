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

// TestRedactionConfigDoesNotNormalizeMode verifies that mode normalization lives
// solely in config defaults/validation. Both config.Load and config.LoadDefault
// validate the config, so by the time New sees it an enabled PIIRedaction always
// carries an explicit "enforce" or "log" mode. The server layer must pass that
// mode through verbatim instead of silently injecting its own default.
func TestRedactionConfigDoesNotNormalizeMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want redaction.Mode
	}{
		{name: "validated enforce mode is preserved", mode: "enforce", want: redaction.ModeEnforce},
		{name: "validated log mode is preserved", mode: "log", want: redaction.ModeLog},
		// An empty mode is rejected by config validation, so the gRPC layer must
		// not paper over it with a default. Previously the dead branch turned it
		// into "enforce" here even though that path is unreachable in production.
		{name: "empty mode is not defaulted by the server", mode: "", want: redaction.Mode("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Default()
			if err != nil {
				t.Fatal(err)
			}
			cfg.PIIRedaction.Enabled = true
			cfg.PIIRedaction.Mode = tt.mode
			cfg.PIIRedaction.Blocklist = nil

			got := redactionConfig(cfg)
			if got.Mode != tt.want {
				t.Fatalf("redaction mode = %q, want %q (server must not normalize mode)", got.Mode, tt.want)
			}
		})
	}
}

// TestRedactionConfigKeepsAllowlistDefault guards the adjacent, still-reachable
// defaulting: an enabled redactor with no allowlist gets the common safe fields.
func TestRedactionConfigKeepsAllowlistDefault(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.PIIRedaction.Enabled = true
	cfg.PIIRedaction.Mode = "enforce"
	cfg.PIIRedaction.Allowlist = nil

	got := redactionConfig(cfg)
	if len(got.Allowlist) == 0 {
		t.Fatal("expected default allowlist when PII redaction is enabled with an empty allowlist")
	}

	cfg.PIIRedaction.Enabled = false
	got = redactionConfig(cfg)
	if len(got.Allowlist) != 0 {
		t.Fatalf("expected no default allowlist when PII redaction is disabled, got %v", got.Allowlist)
	}
}
