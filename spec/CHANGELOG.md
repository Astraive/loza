# Changelog for LOZA Spec

All notable changes to the contract repository are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/0.2.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `spec/services/dedupe_store.py`: a configured but unreachable Redis backend no longer
  silently downgrades to per-process local dedupe. `build_store()` now fails closed with
  `RedisUnavailableError` and logs the underlying error. **Migration:** deployments that
  intentionally relied on the silent fallback must set `DEDUPE_ALLOW_LOCAL_FALLBACK=1` to
  opt back in; the downgrade is then logged as a warning.
- Schema-service image includes the canonical `schemas/json/` directory served by
  `/schemas/<name>`.

## [0.3.4] - 2026-08-26

### Changed

- Updated the shared specification release and all credential examples to the
  `lz_` prefix.


## [0.2.6] - 2026-05-30

### Fixed
- Version bumped to 0.2.6

## [0.2.5] - 2026-05-28

### Fixed
- Version bumped to 0.2.5
- Helm chart and k8s deployment image tags updated to 0.2.5

## [0.2.4] - 2026-05-28

### Fixed
- Version bumped to 0.2.4

## [0.2.0] - 2026-05-20

### Added

- v1 Schema Structure with versioned schema directory
- Core Event Schema (v0.0.1) with required fields
- Optional fields: tenant, trace, user, host, data
- Enriched fields: received_at, collector_version, collector_hostname, ingestion_latency_ms
- Example events
- PII Classification with x-pii extension
- Schema Versioning policy
- Protobuf definitions for collector API
- OpenAPI specification
- JSON Schema definitions
- Conformance fixtures and runner
- Initial protobuf definitions for loza-collector API
- OpenAPI specification
- JSON Schema definitions

### Changed

- Reorganized schema files into versioned directories (v1/)
- Standardized go_package option across all proto files
