# Command Reference

Full reference for all LOZA CLI commands.

## Global Flags

| Flag | Description |
|---|---|
| `--config` | Path to config file (default: `loza-cli.defaults.yaml`) |
| `--verbose` | Enable verbose logging |
| `--output` | Output format: `text`, `json`, `yaml` (default: `text`) |
| `--no-color` | Disable colored output |

---

## Setup Commands

### loza init

Initialize a new LOZA workspace. Creates a default config file and validates the environment.

```bash
loza init
```

**Flags**

| Flag | Description |
|---|---|
| `--dir` | Target directory (default: current directory) |
| `--force` | Overwrite existing config |

**Example**

```bash
loza init --dir ./my-project
```

---

### loza dev

Start a local development environment with collector, Cortex, and a DuckDB backend.

```bash
loza dev
```

**Flags**

| Flag | Description |
|---|---|
| `--port` | Collector port (default: 9308) |
| `--cortex-port` | Cortex port (default: 9312) |
| `--db` | DuckDB file path (default: `loza-local.db`) |

**Example**

```bash
loza dev --port 9308 --cortex-port 9312
```

---

## Collector Commands

### loza collector run

Run the collector binary.

```bash
loza collector run
```

**Flags**

| Flag | Description |
|---|---|
| `-c`, `--config` | Collector config file path |

**Example**

```bash
loza collector run -c configs/loza.local.yaml
```

---

### loza collector version

Print the collector version.

```bash
loza collector version
```

---

### loza collector config print

Print the resolved collector configuration. Secrets (API keys, server secrets,
encryption keys, passwords, DSNs and credential environment references) are
replaced with `<redacted>`, so the output is safe to paste into terminals, CI
logs and support tickets.

```bash
loza collector config print
```

**Example**

```bash
loza collector config print --output json
```

---

### loza collector config validate

Validate a collector config file.

```bash
loza collector config validate
```

**Flags**

| Flag | Description |
|---|---|
| `-c`, `--config` | Config file to validate |

**Example**

```bash
loza collector config validate -c configs/loza.local.yaml
```

---

## Worker Commands

### loza worker run

Run the queue worker for distributed delivery.

```bash
loza worker run
```

**Flags**

| Flag | Description |
|---|---|
| `-c`, `--config` | Worker config file path |

**Example**

```bash
loza worker run -c configs/loza.queue.kafka.yaml
```

---

### loza worker version

Print the worker version.

```bash
loza worker version
```

---

## Config Commands

### loza config print

Print the resolved CLI configuration.

```bash
loza config print
```

**Example**

```bash
loza config print --output yaml
```

---

### loza config validate

Validate the CLI configuration file.

```bash
loza config validate
```

---

## Schema Commands

### loza schema validate

Validate an event payload against the registered schema.

```bash
loza schema validate
```

**Flags**

| Flag | Description |
|---|---|
| `--file` | Event file to validate |
| `--stdin` | Read event from stdin |
| `--schema` | Schema version to validate against |

**Example**

```bash
cat event.json | loza schema validate --stdin
loza schema validate --file event.json --schema v2
```

---

### loza schema fetch

Fetch the current schema from the collector.

```bash
loza schema fetch
```

**Flags**

| Flag | Description |
|---|---|
| `--output` | Output file path |
| `--version` | Schema version to fetch |

**Example**

```bash
loza schema fetch --output schema.json --version v2
```

---

## Emit Commands

### loza emit sample

Create and send a sample event. Use `--print` to inspect it without sending.

**Flags**

| Flag | Description |
|---|---|
| `--service` | Source service name (default: `loza-cli`) |
| `--event` | Event name (default: `sample.event`) |
| `--kind` | Event kind (default: `cli`) |
| `--outcome` | Event outcome (default: `success`) |
| `--level` | Event level (default: `info`) |
| `--attrs` | Custom attributes as a JSON object |
| `--print` | Print the envelope without sending it |
| `--output` | Write the envelope to a file |

**Example**

```bash
loza emit sample --service payment-service --event payment.failed --kind http_request \
  --outcome error --level error --print \
  --attrs '{"status_code":500,"path":"/api/charge","latency_ms":4500}'
```

---

## Query Commands

### loza query

The `-q` value is LQL by default. Add `--raw-sql` to execute explicit SQL against
the collector's DuckDB storage; `--engine` selects the SQL engine.

**Flags**

| Flag | Description |
|---|---|
| `-q` | LQL query or, with `--raw-sql`, explicit SQL (required) |
| `--raw-sql` | Interpret `-q` as SQL instead of LQL |
| `--engine` | SQL engine for `--raw-sql` (default: `duckdb`) |
| `--format` | Output format: `table`, `json`, or `csv` |
| `--limit` | Maximum rows for LQL queries (0 uses the collector default) |
| `--param` | Typed LQL parameter in `key=value` form (repeatable) |

**Examples**

```bash
loza query -q "FROM events" --limit 10 --format json
loza query -q "SELECT service, count(*) FROM events GROUP BY service ORDER BY count(*) DESC" \
  --raw-sql --engine duckdb --format json
```

---

### loza tail

Stream matching events in real time from the collector.

**Flags**

| Flag | Description |
|---|---|
| `--kind` | Filter by event kind |
| `--service` | Filter by service name |
| `--level` | Filter by level |

**Example**

```bash
loza tail --service payment-service --kind http_request --level error
```

---

### loza watch

Watch matching events in real time.

**Flags**

| Flag | Description |
|---|---|
| `--kind` | Filter by event kind |
| `--service` | Filter by service name |
| `--level` | Filter by level |

**Example**

```bash
loza watch --service payment-service --kind http_request --level error
```

---

## Status Commands

### loza status

Show collector status: uptime, version, sinks, event counts.

```bash
loza status
```

**Example**

```bash
loza status --output json
```

---

### loza sinks

List configured sinks and their status. Supports explicit subcommands:
- `loza sinks list`
- `loza sinks show <name>`
- `loza sinks test <name>`

```bash
loza sinks
```

**Example**

```bash
loza sinks --output json
```

```bash
loza sinks test duckdb
```

---

### loza doctor

Run health checks against the collector.

```bash
loza doctor
```

Checks: connectivity, readiness, schema compatibility, sink health, DLQ status.

**Example**

```bash
loza doctor --verbose
```

---

## DLQ Commands

### loza dlq

Inspect the Dead Letter Queue.

```bash
loza dlq
```

**Flags**

| Flag | Description |
|---|---|
| `--limit` | Max entries to show (default: 20) |
| `--output` | Output format |

**Example**

```bash
loza dlq --limit 50 --output json
```

---

### loza replay

Replay events from the Dead Letter Queue.

```bash
loza replay
```

**Flags**

| Flag | Description |
|---|---|
| `--all` | Replay all DLQ entries |
| `--id` | Replay a specific DLQ entry by ID |
| `--limit` | Max entries to replay |

**Example**

```bash
loza replay --all
loza replay --id dlq-001
```

---

## GDPR Commands

### loza delete tenant

Delete all data for a tenant.

```bash
loza delete tenant <tenant_id>
```

**Example**

```bash
loza delete tenant acme-corp
```

---

### loza delete user

Delete all data for a user.

```bash
loza delete user <user_id>
```

**Example**

```bash
loza delete user user-12345
```

---

### loza delete event

Delete a specific event.

```bash
loza delete event <event_id>
```

**Example**

```bash
loza delete event evt-abc123
```

---

## Audit Commands

### loza audit

Run a PII audit on stored events.

```bash
loza audit
```

**Flags**

| Flag | Description |
|---|---|
| `--service` | Audit a specific service |
| `--since` | Audit events since a time (e.g., `7d`) |
| `--output` | Output format |

**Example**

```bash
loza audit --service payment-service --since 30d --output json
```

---

### loza export

Export events from the collector.

```bash
loza export
```

**Flags**

| Flag | Description |
|---|---|
| `--output` | Output file path (required) |
| `--format` | Export format: `json`, `csv`, `ndjson` (default: `ndjson`) |
| `--service` | Filter by service |
| `--since` | Export events since a time |
| `--limit` | Max events to export |

**Example**

```bash
loza export --output events.json --format json --service payment-service --since 7d
```

---

## Cortex Commands

### loza cortex

Query the Cortex control plane.

```bash
loza cortex
```

**Flags**

| Flag | Description |
|---|---|
| `--url` | Cortex URL (default: from config) |

---

### loza incident

Reconstruct an incident.

```bash
loza incident <incident_id>
```

**Flags**

| Flag | Description |
|---|---|
| `--mode` | Reconstruction mode: `fast`, `deep` (default: `fast`) |
| `--output` | Output format |

**Example**

```bash
loza incident inc-001 --mode deep --output json
```

---

### loza graph

Query the service graph.

```bash
loza graph <service>
```

**Flags**

| Flag | Description |
|---|---|
| `--depth` | Graph traversal depth (default: 3) |
| `--output` | Output format |

**Example**

```bash
loza graph payment-service --depth 5 --output json
```

---

### loza signatures

Search for similar incident signatures.

```bash
loza signatures
```

**Flags**

| Flag | Description |
|---|---|
| `--symptoms` | Comma-separated symptom list |
| `--services` | Comma-separated service list |
| `--limit` | Max results (default: 10) |
| `--output` | Output format |

**Example**

```bash
loza signatures --symptoms "timeout,5xx_spike" --services "payment-service" --limit 5
```

---

## Debug Commands

### loza debug

Debug utilities for troubleshooting.

```bash
loza debug
```

**Flags**

| Flag | Description |
|---|---|
| `--pprof` | Enable pprof endpoint |
| `--dump-config` | Dump resolved config |

**Example**

```bash
loza debug --dump-config --output json
```

---

### loza bench

Run benchmarks against the collector.

```bash
loza bench
```

**Flags**

| Flag | Description |
|---|---|
| `--rate` | Events per second (default: 1000) |
| `--duration` | Test duration (default: `10s`) |
| `--connections` | Number of concurrent connections (default: 10) |

**Example**

```bash
loza bench --rate 10000 --duration 60s --connections 50
```

---

## Deploy Commands

### loza deploy

Deploy LOZA components.

```bash
loza deploy
```

**Flags**

| Flag | Description |
|---|---|
| `--target` | Deployment target: `docker`, `k8s`, `local` |
| `--component` | Component to deploy: `collector`, `cortex`, `all` |

**Example**

```bash
loza deploy --target k8s --component collector
```

---

### loza dashboard

Open or manage the LOZA dashboard.

```bash
loza dashboard
```

**Flags**

| Flag | Description |
|---|---|
| `--port` | Dashboard port (default: 3000) |
| `--open` | Open in browser |

**Example**

```bash
loza dashboard --port 8080 --open
```

---

## Maturity Command

### loza maturity

Display the maturity status of each LOZA component.

```bash
loza maturity
```

Shows the version, status, and feature completeness of the collector, cortex, CLI, and SDKs.
