# Changelog

All notable changes to the LOZA Rust SDK are documented in this file.

## [Unreleased]

### Fixed

- PascalCase `Configure(config)` now installs and returns the global logger like
  `configure(config)`, with both APIs returning `Result<Logger, LozaError>`.

- Event mutators no longer discard the `ensure_mutable` lifecycle check:
  `set_attr`, `append_attr`, `merge_group`, `delete_attr`,
  `checkpoint_with_attrs`, `start_process`, `start_timer`, `start_group`, and
  the matching `Logger` helpers are now no-ops once an event is emitting,
  emitted, or failed. Public signatures are unchanged.
### Changed

- Sensitive-value hashing (`HashKeys` redaction and the `Hash`/`EmailHash`/`IPHash`
  attribute helpers) now uses keyed HMAC-SHA256 instead of unsalted SHA-256.
  Plain SHA-256 digests of low-entropy values were reversible with rainbow
  tables and did not provide real redaction. Set `LOZA_HASH_SECRET` to a stable
  secret shared by all producers when hashes must remain comparable across
  processes or restarts; when it is unset, a random per-process key is used.
### Security

- Scope the default storage/spool directory to the current user instead of a
  fixed name in the shared temp dir, and add `create_default_storage_dir()` to
  create it with `0700` permissions while refusing symlinks. `default_storage_dir()`
  keeps its `PathBuf` return type; call the new creation helper before writing.
- `parse_lines` no longer silently discards malformed NDJSON lines. It now
  returns `Result<Vec<Value>, NdjsonError>`, reporting the 1-based line number
  and parse error of the first invalid line. Blank lines are still ignored.

### Migration note
- **Breaking:** callers of `parse_lines` must handle the returned `Result`.
  Migration: `parse_lines(input)` previously returned `Vec<Value>` and dropped
  invalid lines; replace `let values = parse_lines(input);` with
  `let values = parse_lines(input)?;` (or `.expect(...)` / `.unwrap_or_default()`
  to preserve the old lenient behavior). `NdjsonError` is exported alongside
  `parse_lines` from `loza::utils`.

## [0.3.4] - 2026-08-26

### Changed

- Updated the Rust SDK release version and credential examples to the `lz_`
  prefix.

## [0.2.6] - 2026-05-30

### Fixed
- Fix RwLock poisoning recovery in global logger
- Fix Mutex poisoning recovery in metrics
- Bump version to 0.2.6

## [0.2.5] - 2026-05-28

### Fixed
- Version bumped to 0.2.5
- SDK parity manifest updated to 0.2.5

## [0.2.4] - 2026-05-28

### Fixed
- Version bumped to 0.2.4

## [0.0.2] - 2026-05-20

### Added

- Initial release of the LOZA Rust SDK.
- Core event lifecycle: `start_event` -> `enrich` -> `finish` -> `emit`.
- Logger with async/sync delivery via configurable sinks.
- `StdoutSink`, `StderrSink`, `FileSink`, `MemorySink`, `NoopSink`, `HTTPBatchSink`.
- 14-key safety-net redactor (`DefaultRedactor`).
- Full sampler suite: `SampleAll`, `SampleNone`, `SampleRandom`, `SampleErrors`, `SampleSlowRequests`, `SampleStatusCodes`, `SampleRoutes`, `SampleUsers`, `SampleTenants`, `SampleFeatureFlag`, `SampleByHeader`.
- Sampler combinators: `AnySampler`, `AllSampler`, `NotSampler`.
- Schema support: `DefaultSchema`, `FlatSchema`, `NestedSchema`, `ECSchema`, `OTelSchema`, `DatadogSchema`, `CustomSchema`.
- Redaction: `RedactKeys`, `HashKeys`, `DropKeys`, `MaskKeys`, `RedactPatterns`, `ComposeRedactors`.
- Attribute constructors: `String`, `Int`, `Int64`, `Uint64`, `Float64`, `Bool`, `Time`, `Duration`, `Any`, `Null`, `Group`, `SensitiveString`, `HashString`, `MarkSensitive`.
- Canonical attribute helpers: `UserID`, `TenantID`, `RequestID`, `TraceID`, `SpanID`, and 20+ domain-specific helpers.
- `MetricsCollector` and Prometheus rendering.
- `CortexClient` for Persistent Context Engine integration.
- Tower middleware for HTTP request capture.
- Spec contract from `spec/`.
- Both PascalCase (Go-style) and snake_case (Rust-idiomatic) public API.
