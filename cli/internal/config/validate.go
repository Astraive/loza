package config

import (
	"fmt"
	"strings"
)

var validValidationModes = map[string]bool{
	"off":        true,
	"warn":       true,
	"enforce":    true,
	"quarantine": true,
}

func Validate(cfg Config) error {
	if strings.TrimSpace(cfg.CollectorRepoPath) == "" {
		return fmt.Errorf("collector_repo_path must not be empty")
	}
	if strings.TrimSpace(cfg.SpecRepoPath) == "" {
		return fmt.Errorf("spec_repo_path must not be empty")
	}
	if strings.TrimSpace(cfg.CollectorURL) == "" {
		return fmt.Errorf("collector_url must not be empty")
	}
	if cfg.Cortex != nil && strings.TrimSpace(cfg.Cortex.URL) == "" && strings.TrimSpace(cfg.CortexRepoPath) == "" {
		return fmt.Errorf("cortex.url or cortex_repo_path must be configured when cortex is enabled")
	}
	return ValidateValidationMode(cfg.ValidationMode)
}

// ValidateValidationMode reports whether mode is a supported validation mode.
// An empty mode is allowed and means "use the default". This is kept separate
// from Validate so callers that only need to check a single mode (for example,
// before generating a config file) do not have to supply the whole Config.
func ValidateValidationMode(mode string) error {
	if mode == "" {
		return nil
	}
	if !validValidationModes[strings.ToLower(strings.TrimSpace(mode))] {
		return fmt.Errorf("validation_mode must be one of: off, warn, enforce, quarantine")
	}
	return nil
}
