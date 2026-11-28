# Changelog

## [Unreleased]

### Fixed

- Loguru integration now reads level names from Loguru's attribute-based level
  objects, as well as mapping- and string-based levels.


### Security

- `email_hash` and `ip_hash` now pseudonymize with keyed HMAC-SHA256 instead of
  unkeyed SHA-256. Supply a private key with `LOZA_PII_HASH_SECRET` or the
  `secret=` keyword; otherwise a random per-process key is used.

### Migration

- Pseudonym values are no longer equal to the plain SHA-256 digest of the
  normalized value. Consumers that stored or compared previous hashes must
  re-baseline them and should configure `LOZA_PII_HASH_SECRET` to keep
  pseudonyms stable across processes and restarts. Existing call sites keep
  working because the key is an optional keyword argument.
### Changed
- `HTTPBatchSink` NDJSON mode now validates each event and rejects empty
  batches, matching the JSON ingest path. Callers that relied on sending raw,
  unvalidated lines or empty batches must now provide valid event JSON objects.
- Value hashing (`HashKeys`, `HashString`/`hash_value` and `hash_value=True`
  attributes) now uses keyed HMAC-SHA256 instead of unsalted SHA-256, so
  low-entropy PII cannot be recovered with rainbow tables. Set
  `LOZA_HASH_SECRET` to share a stable key across processes; without it a
  random per-process key is used and hashes are not stable across restarts.

## [0.3.4] - 2026-08-26

### Changed

- Updated the Python SDK release version and credential examples to the
  `lz_` prefix.

## [0.2.6] - 2026-05-30

### Security
- Fix path traversal in DiskOfflineBuffer, FileSink, RotatingFileSink
- Fix SSRF in collector endpoint validation
- Fix Cortex client endpoint SSRF validation

### Fixed
- Fix config with_* methods to be non-mutating
- Fix flush() to respect timeout parameter
- Fix FileSink and DiskOfflineBuffer file handle leaks
- Fix Starlette middleware to use async HTTP
- Fix DeliveryStats thread safety
- Add response body caps in HTTP clients
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
- Initial stable release
- Core event lifecycle: StartEvent, Enrich, Finish, Emit
- Logger with async/sync delivery
- HTTPBatchSink with gzip compression and retry
- StdoutSink, FileSink, MemorySink, NoopSink
- 14-key safety-net redactor
- Full sampler suite
- DefaultSchema and FlatSchema
- Spec contract from spec/
- Middleware: asgi, django, fastapi, flask, starlette
- Integrations: logging, loguru, structlog, otel
