package grpcserver

import (
	"github.com/astraive/loza/cortex/internal/api"
	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/middleware"
	"github.com/astraive/loza/cortex/internal/redaction"
	"github.com/astraive/loza/cortex/internal/storage"
	"google.golang.org/grpc"
)

func New(cfg *config.Config, stor storage.Storage, graphStore ...storage.GraphStore) *api.GRPCServer {
	// Redaction is derived from cfg.PIIRedaction inside NewGRPCServer so every
	// ingestion path shares the same configured redactor.
	return api.NewGRPCServer(cfg, stor, graphStore...)
}

// buildRedactConfig resolves the effective redaction settings from config.
// When redaction is enabled and no mode is explicitly configured, enforcement
// is the safe default regardless of whether a blocklist is present.
func buildRedactConfig(cfg *config.Config) redaction.Config {
	redactCfg := redactionConfig(cfg)
	if cfg.PIIRedaction.Enabled && redactCfg.Mode == "" {
		redactCfg.Mode = redaction.ModeEnforce
	}
	return redactCfg
}

func redactionConfig(cfg *config.Config) redaction.Config {
	redactCfg := redaction.Config{
		Mode:      redaction.Mode(cfg.PIIRedaction.Mode),
		Blocklist: cfg.PIIRedaction.Blocklist,
		Allowlist: cfg.PIIRedaction.Allowlist,
	}
	if len(redactCfg.Allowlist) == 0 && cfg.PIIRedaction.Enabled {
		// Default allowlist for common non-sensitive event fields
		redactCfg.Allowlist = []string{"service", "level", "timestamp", "id", "event_id", "trace_id", "span_id", "incident_id", "environment", "release", "version", "schema_version", "event_version"}
	}
	return redactCfg
}

// ServerOptions returns the gRPC server options that install the shared auth,
// role and rate-limit interceptors. The server binary applies these at
// construction so every registered RPC is protected by default.
func ServerOptions(cfg *config.Config) []grpc.ServerOption {
	if cfg == nil {
		return nil
	}

	var rateLimit *middleware.RateLimiter
	if cfg.RateLimit.Enabled {
		rateLimit = middleware.NewRateLimiter(cfg.RateLimit.PerAPIKeyRPM, cfg.RateLimit.PerIPRPM)
	}

	interceptor := middleware.NewGRPCAuthInterceptor(middleware.NewAuth(&cfg.Authentication), rateLimit)
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(interceptor.Unary),
		grpc.ChainStreamInterceptor(interceptor.Stream),
	}
}
