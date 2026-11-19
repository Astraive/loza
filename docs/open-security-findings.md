# Open Security Findings (living tracker)

> This is the authoritative list of security work that is still open in LOZA. It supersedes the
> historical v0.2.4-era reports, which are marked **Status: HISTORICAL** and must not be used to
> triage current risk. Current release: **0.4.1** ([CHANGELOG.md](../CHANGELOG.md)).
>
> Maintained as part of [issue #60](https://github.com/Astraive/loza/issues/60). When a finding is
> fixed, move it to *Fixed since v0.2.4* with a link to the fixing PR. When new work is found, add a
> row with a link to its tracking issue (open one if none exists).

## Open findings

| ID | Severity | Finding | Evidence on current `main` | Tracking |
|----|----------|---------|----------------------------|----------|
| OPEN-001 | Medium | `AuditLogger` is defined but has zero production call sites, so auth-sensitive actions are never emitted as audit events. | `collector/internal/auth/audit.go` defines `AuditLogger` / `SlogAuditLogger`; no production code calls `LogAudit` or `NewSlogAuditLogger`. `docs/security.md#audit-events` currently claims the events are emitted — that claim is aspirational until this is wired. | No dedicated issue yet — [search issues](https://github.com/Astraive/loza/issues?q=AuditLogger) |
| OPEN-002 | Medium | Collector `/graphql` has no request body size limit, so an oversized JSON body is read in full before any validation. | `collector/internal/server/graphql.go` decodes `r.Body` with `json.NewDecoder` and no `http.MaxBytesReader`/`io.LimitReader`. (The *query depth* limit is present: `maxGraphQLDepth = 10` plus `queryDepth()`.) | No dedicated issue yet — [search issues](https://github.com/Astraive/loza/issues?q=graphql+body+limit) |
| OPEN-003 | Medium | Cortex GraphQL/gRPC traversal depth is unbounded and the WebSocket path bypasses the depth check. | Tracked separately; *Cortex*, not the collector depth check referenced in the old reports. | [#52](https://github.com/Astraive/loza/issues/52) |

## Fixed since v0.2.4 (see historical reports for original context)

| Finding | Current state |
|---------|---------------|
| Blueprint DuckDB type injection (`POST /schema/blueprint`) | Complete DuckDB type string validated against an allowlist, with safe-character validation for type parameters (`collector/cmd/loza-collector/schema_audit_handlers.go`). |
| Empty-key auth bypass | `authorizeAPIKey` returns `false` when no API key is configured (`collector/cmd/loza-collector/auth.go`). |
| `GET /status` legacy auth | Multi-key middleware applied to `/status`. |
| Missing HSTS header | `Strict-Transport-Security` set by both collector and cortex middleware. |
| Deprecated `X-XSS-Protection` header | Removed. |
| Wrong Dockerfile `EXPOSE` ports | Collector exposes 9308–9311; cortex exposes 9312/9313. |
| SDK ingest envelopes stamped `0.2.0` | SDKs stamp the current version; no hardcoded `0.2.0` defaults remain in SDK runtime source. |
| LQL `wasm.ts` SQL injection in lozana | `wasm.ts` is a harmless editor adapter. |
| Collector GraphQL query depth unbounded (old finding #3) | Depth limit added in v0.2.5: `maxGraphQLDepth = 10` enforced in `collector/internal/server/graphql.go`. |
| "Cross-SDK conformance runner missing" (`benchmark-report.md` FINDING-007) | `spec/conformance/runner.py` exists and is run by `.github/workflows/spec-ci.yml`; consistent with the 48/48 PASS result in `regression-report.md`. |

## Reconciled contradictions in the historical reports

- **Conformance runner:** `benchmark-report.md` FINDING-007 says `spec/conformance/runner.py` is
  missing; `regression-report.md` reports 48/48 checks PASS using that same runner. The runner
  exists on current `main`, so the regression result is the accurate one and FINDING-007 is stale.
- **Version numbers:** `regression-report.md` and `benchmark-report.md` are titled "v0.2.0" while
  `api-security-v0.2.4.md` and `code-review-final-v0.2.4.md` cover v0.2.4. All four predate the
  current release, 0.4.1. Original titles are preserved in the historical artifacts; this tracker
  is the source of truth for current status.
- **Audit events:** `docs/security.md` states that all auth-sensitive actions emit audit events,
  but the `AuditLogger` has no production call site (OPEN-001). Treat that statement as a target
  state, not a shipped guarantee.
