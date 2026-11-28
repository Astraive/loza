# Sink Reference

The collector's fanout stage supports seven storage and export outputs. Each output receives events through the fanout stage.

## Configuration

Configure outputs under `fanout.outputs`. Each entry has common `name`, `role`, `type`, and `enabled` fields. Type-specific settings belong under a nested block named for the output type.

```yaml
fanout:
  outputs:
    - name: primary-db
      role: primary
      type: duckdb
      enabled: true
      duckdb:
        path: loza.db
        table: events
```

## Sink Overview

| Sink | Type | Batching | Durability | Use Case |
|---|---|---|---|---|
| DuckDB | Columnar embedded DB | Micro-batch | File-based | Local query, analytics |
| ClickHouse | Columnar OLAP | Micro-batch | Replicated | Analytics, aggregations |
| Postgres | Relational DB | Per-message | WAL | Metadata, relational queries |
| Loki | Log aggregation | Micro-batch | Replicated | Log search, dashboards |
| OTLP | OpenTelemetry export | Batch | Protocol-level | Vendor-agnostic export |
| S3 | Object storage | Micro-batch | Replicated | Cold storage, archival |
| GCS | Object storage | Micro-batch | Replicated | Cold storage, archival |

Kafka is configured separately as the event bus for queue or hybrid mode; it is not a fanout output. See the [Kafka queue example](../configs/loza.queue.kafka.yaml).

---

## DuckDB

Embedded columnar database. The default sink for local development and single-node deployments. Shared with Cortex for incident intelligence.

**Collector YAML fields (`fanout.outputs[].duckdb`)**

| Field | Type | Description |
|---|---|---|
| `path` | string | Database file path |
| `table` | string | Target table name |
| `raw_column` | string | Raw event JSON column |
| `store_raw` | bool | Store raw event JSON |

**Example**

```yaml
fanout:
  outputs:
    - name: local-db
      role: primary
      type: duckdb
      enabled: true
      duckdb:
        path: /data/loza.db
        table: events
        store_raw: true
```

**Behavior Notes**

- Events are inserted in micro-batches for throughput.
- The database file is locked per-process. Do not run multiple collector instances against the same file.
- Cortex opens the same database file for read queries.
- Retention is managed by the `storage.duckdb.retention_days` config.

---

## Kafka Event Bus

Kafka is not a fanout sink, so `type: kafka` is not supported under
`fanout.outputs`. Queue and hybrid modes use the top-level `kafka` settings;
see the [queue-mode configuration](../configs/loza.queue.kafka.yaml).

---

## ClickHouse

Columnar OLAP database for analytics workloads.

**Collector YAML fields (`fanout.outputs[].clickhouse`)**

| Field | Type | Description |
|---|---|---|
| `addrs` | string[] | ClickHouse server addresses |
| `database` | string | Database name |
| `username` | string | User name |
| `password` | string | Password |
| `table` | string | Target table |
| `schema` | map[string]string | Column type overrides |
| `store_raw` | bool | Store raw event JSON |
| `raw_column` | string | Raw event JSON column |

**Example**

```yaml
fanout:
  outputs:
    - name: analytics
      role: secondary
      type: clickhouse
      enabled: true
      clickhouse:
        addrs:
          - "127.0.0.1:9000"
        database: loza
        table: events
```

**Behavior Notes**

- Events are batched and inserted using `INSERT INTO ... FORMAT JSONEachRow`.
- The schema SQL is in `internal/sinks/clickhouse/schema.sql`.
- Retries are handled at the batch level.

---

## Postgres

Relational database for metadata storage and relational queries.

**Collector YAML fields (`fanout.outputs[].postgres`)**

| Field | Type | Description |
|---|---|---|
| `dsn` | string | PostgreSQL connection DSN |
| `table` | string | Target table name |
| `schema` | map[string]string | Column type overrides |
| `store_raw` | bool | Store raw event JSON |
| `raw_column` | string | Raw event JSON column |

**Example**

```yaml
fanout:
  outputs:
    - name: metadata
      role: secondary
      type: postgres
      enabled: true
      postgres:
        dsn: "postgres://user:pass@localhost:5432/loza?sslmode=disable"
        table: events
```

**Behavior Notes**

- Events are inserted using prepared statements.
- The schema SQL is in `internal/sinks/postgres/schema.sql`.
- Connection pooling is managed by `pgxpool`.

---

## Loki

Log aggregation system for log search and dashboard integration.

**Collector YAML fields (`fanout.outputs[].loki`)**

| Field | Type | Description |
|---|---|---|
| `url` | string | Loki push URL |
| `tenant_id` | string | Loki tenant ID (`X-Scope-OrgID`) |
| `labels` | map[string]string | Static labels |
| `batch_size` | int | Push batch size |
| `flush_interval` | duration | Push interval |
| `timeout` | duration | Request timeout |

**Example**

```yaml
fanout:
  outputs:
    - name: logs
      role: secondary
      type: loki
      enabled: true
      loki:
        url: "http://localhost:3100/loki/api/v1/push"
        tenant_id: loza
        labels:
          source: loza-collector
```

**Behavior Notes**

- Events are converted to Loki log entries with labels extracted from event fields.
- Pushes use the `/loki/api/v1/push` endpoint.
- Retries are handled at the batch level.

---

## OTLP

OpenTelemetry Protocol export for vendor-agnostic event forwarding.

**Collector YAML fields (`fanout.outputs[].otlp`)**

| Field | Type | Description |
|---|---|---|
| `endpoint` | string | OTLP gRPC or HTTP endpoint |
| `protocol` | string | Transport: `grpc` or `http` |
| `insecure` | bool | Disable TLS |
| `timeout` | duration | Export request timeout |
| `logger` | string | Logger name |

**Example**

```yaml
fanout:
  outputs:
    - name: otlp-export
      role: secondary
      type: otlp
      enabled: true
      otlp:
        endpoint: "otel-collector:4317"
        protocol: grpc
        insecure: true
```

**Behavior Notes**

- Events are converted to OTLP log records using `internal/otlpconv`.
- Supports both gRPC and HTTP transports.
- Compatible with any OTLP-compliant backend (Jaeger, Grafana Tempo, etc.).

---

## S3

AWS S3 object storage for cold storage and archival.

**Collector YAML fields (`fanout.outputs[].s3`)**

| Field | Type | Description |
|---|---|---|
| `bucket` | string | S3 bucket name |
| `prefix` | string | Object key prefix |
| `region` | string | AWS region |
| `endpoint` | string | Optional custom endpoint |
| `access_key` | string | Optional access key |
| `secret_key` | string | Optional secret key |
| `schema` | map[string]string | Column type overrides |
| `batch_size` | int | Objects per flush |
| `flush_interval` | duration | Flush interval |

**Example**

```yaml
fanout:
  outputs:
    - name: archive
      role: secondary
      type: s3
      enabled: true
      s3:
        bucket: loza-events
        prefix: "2026/05/"
        region: us-east-1
        flush_interval: 60s
```

**Behavior Notes**

- Events are batched into NDJSON files and uploaded as S3 objects.
- Object keys follow the pattern: `{prefix}{date}/{batch_id}.jsonl`
- Uses the AWS SDK v2 for Go. Credentials are resolved from the standard AWS chain.

---

## GCS

Google Cloud Storage for cold storage and archival.

**Collector YAML fields (`fanout.outputs[].gcs`)**

| Field | Type | Description |
|---|---|---|
| `bucket` | string | GCS bucket name |
| `prefix` | string | Object key prefix |
| `credentials_file` | string | Service account credentials file |
| `schema` | map[string]string | Column type overrides |
| `batch_size` | int | Objects per flush |
| `flush_interval` | duration | Flush interval |

**Example**

```yaml
fanout:
  outputs:
    - name: gcs-archive
      role: secondary
      type: gcs
      enabled: true
      gcs:
        bucket: loza-events-gcs
        prefix: events/
```

**Behavior Notes**

- Events are batched into NDJSON files and uploaded as GCS objects.
- Uses the Google Cloud Go client library. Credentials are resolved from the standard GCP chain (service account, workload identity, etc.).
- Object naming follows the same pattern as S3.
