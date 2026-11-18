package main

import (
	"context"
	"math/rand"
	"strings"
	"time"

	processing "github.com/astraive/loza/collector/internal/processing"
)

// config returns a consistent snapshot of the runtime configuration.
// A SIGHUP reload swaps cfg concurrently with request handling, so callers
// must never read s.cfg directly. The returned value shares the config's
// backing slices/maps, which are treated as immutable after publication.
func (s *collectorState) config() collectorConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// setConfig atomically publishes a new runtime configuration snapshot.
func (s *collectorState) setConfig(cfg collectorConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *collectorState) isReady() bool {
	return s.ready.Load() && s.effectiveSinkHealthy() && s.effectiveSpoolHealthy() && s.effectiveDiskHealthy()
}

func (s *collectorState) effectiveSinkHealthy() bool {
	if s.sinkHealthy.Load() {
		return true
	}
	return s.metrics.sinkWriteErrors.Load() == 0
}

func (s *collectorState) effectiveSpoolHealthy() bool {
	cfg := s.config()
	if cfg.reliabilityMode != "spool" && cfg.reliabilityMode != "hybrid" {
		return true
	}
	if s.spoolHealthy.Load() {
		return true
	}
	return s.metrics.spoolBytes.Load() == 0
}

func (s *collectorState) effectiveDiskHealthy() bool {
	// diskHealthy is driven exclusively by disk-full signals (the OnDiskFull
	// callbacks and spool-write disk-full errors). A sink/DLQ write error is a
	// separate condition and must not be used to infer disk health: a disk-full
	// signal that does not also increment sinkWriteErrors would otherwise be
	// reported as healthy, keeping isReady() and the readiness metric green.
	return s.diskHealthy.Load()
}

func (s *collectorState) ensureProcessor() error {
	s.processorMu.Lock()
	defer s.processorMu.Unlock()
	if s.processorClosed {
		return errProcessorClosed
	}
	if s.processor != nil {
		return nil
	}
	if s.rng == nil {
		s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	cfg := s.config()
	processor, err := processing.New(processing.Config{
		DeliveryPolicy:          cfg.deliveryPolicy,
		RetryEnabled:            cfg.retryEnabled,
		RetryMaxAttempts:        cfg.retryMaxAttempts,
		RetryInitialBackoff:     cfg.retryInitialBackoff,
		RetryMaxBackoff:         cfg.retryMaxBackoff,
		RetryJitter:             cfg.retryJitter,
		FallbackEnabled:         cfg.fallbackEnabled,
		FallbackOnPrimaryFail:   cfg.fallbackOnPrimaryFail,
		FallbackOnSecondaryFail: cfg.fallbackOnSecondaryFail,
		FallbackOnPolicyFail:    cfg.fallbackOnPolicyFail,
		DLQEnabled:              cfg.dlqEnabled,
		DLQPath:                 cfg.dlqPath,
		DLQOnPrimaryFail:        cfg.dlqOnPrimaryFail,
		DLQOnSecondaryFail:      cfg.dlqOnSecondaryFail,
		DLQOnFallbackFail:       cfg.dlqOnFallbackFail,
		DLQOnPolicyFail:         cfg.dlqOnPolicyFail,
		DedupeEnabled:           s.dedupeEnabled(),
		DedupeKey:               cfg.dedupeKey,
		DedupeWindow:            cfg.dedupeWindow,
		OnDiskFull: func() {
			s.diskHealthy.Store(false)
		},
		Schema: processing.SchemaConfig{
			Mode:           schemaModeForProcessor(s),
			SchemaVersion:  cfg.schemaSchemaVersion,
			EventVersion:   cfg.schemaEventVersion,
			QuarantinePath: cfg.schemaQuarantinePath,
			Registry:       s.convertSchemaRegistry(),
		},
		Privacy: processing.PrivacyConfig{
			Mode:       cfg.privacyMode,
			Blocklist:  append([]string(nil), cfg.privacyBlocklist...),
			Allowlist:  append([]string(nil), cfg.privacyAllowlist...),
			SecretScan: cfg.secretScan,
		},
		LogFunc: func(level string, message string, fields map[string]any) {
			logJSON(level, message, fields)
		},
	}, s.ingestSink, s.secondarySinks, s.fallbackSink, s.rng)
	if err != nil {
		return err
	}
	s.processor = processor
	return nil
}

func schemaModeForProcessor(s *collectorState) string {
	if !s.schemaValidationEnabled() {
		return "off"
	}
	return s.config().schemaMode
}

func (s *collectorState) convertSchemaRegistry() []processing.SchemaRegistryEntry {
	if len(s.config().schemaRegistry) == 0 {
		return nil
	}
	out := make([]processing.SchemaRegistryEntry, len(s.config().schemaRegistry))
	for i, e := range s.config().schemaRegistry {
		out[i] = processing.SchemaRegistryEntry{
			SchemaVersion:  e.SchemaVersion,
			EventVersion:   e.EventVersion,
			RequiredFields: e.RequiredFields,
		}
	}
	return out
}

func (s *collectorState) isDuplicate(value string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	if s.dedupeStore != nil {
		seen, err := s.dedupeStore.SeenBefore(context.Background(), value, s.config().dedupeWindow)
		if err != nil {
			return false, err
		}
		return seen, nil
	}
	s.dedupeMu.Lock()
	defer s.dedupeMu.Unlock()
	now := time.Now()
	for k, ts := range s.dedupeSeenAt {
		if now.Sub(ts) > s.config().dedupeWindow {
			delete(s.dedupeSeenAt, k)
		}
	}
	if ts, ok := s.dedupeSeenAt[value]; ok && now.Sub(ts) <= s.config().dedupeWindow {
		return true, nil
	}
	s.dedupeSeenAt[value] = now
	return false, nil
}
