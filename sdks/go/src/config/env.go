package config

import "github.com/astraive/loza/sdks/go/src/core"

// LoadFromEnv loads configuration from environment variables. Parse errors are
// ignored; use LoadFromEnvE to surface configuration errors to callers.
func LoadFromEnv(cfg Config) Config { return core.LoadFromEnv(cfg) }

// LoadFromEnvE loads configuration from environment variables and returns any
// parse failures so callers can surface misconfiguration.
func LoadFromEnvE(cfg Config) (Config, error) { return core.LoadFromEnvE(cfg) }
