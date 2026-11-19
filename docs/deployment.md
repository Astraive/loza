# LOZA Deployment Guide

This guide documents the deployment paths that are implemented in this repo today.

## Local Development

Run the collector directly:

```bash
cd collector
go run ./cmd/loza-collector run -c configs/loza.local.yaml
```

Run the worker when you are testing queue mode:

```bash
cd collector
go run ./cmd/loza-worker run
```

Run the CLI against the same workspace:

```bash
cd cli
go run ./cmd/loza query --sql "SELECT * FROM events LIMIT 10"
```

## Packaged Assets

Current deployment assets are split by ownership:

- Collector runtime assets live under [collector/deploy](collector/deploy)
- `schema-service` and `stager` container and Helm assets live under [spec](spec), with the canonical chart at [spec/charts/loza](spec/charts/loza)
- Root-level [docker-compose.yml](docker-compose.yml) is the local Kafka/Redis/schema-service/stager integration stack

Treat `spec/deploy/k8s` as thin example manifests that mirror the chart-managed deploy path.

## Health and Metrics

Current HTTP endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `GET /core/status`

## Authentication

Current enforced HTTP auth is API-key based.

Example:

```yaml
auth:
  enabled: true
  server_secret: "${COLLECTOR_SERVER_SECRET}"
  keys:
    - name: "prod"
      key_id: "k2M9aQp"
      secret_env: "PROD_KEY_SECRET"
      kind: "sec"
      roles: ["collector_ingest_server"]
```

See [Authentication](authentication.md) and [Authorization](authorization.md) for full auth configuration.

JWT and mTLS appear in configuration and backlog material, but they are not the current HTTP ingest contract to rely on for release usage.

## Cortex TLS (HTTP and gRPC)

The Cortex server applies the top-level `tls` block to both its HTTP API and its
gRPC listener. When `tls.enabled: true`, both listeners use TLS with the same
settings; an incomplete or invalid configuration fails startup instead of
silently falling back to plaintext.

```yaml
tls:
  enabled: true
  cert_file: /etc/loza/tls/server.pem
  key_file: /etc/loza/tls/server-key.pem
  min_version: "1.2"      # "1.2" (default) or "1.3"
  mutual_tls: true
  client_ca_file: /etc/loza/tls/ca.pem
```

- `cert_file` and `key_file` must contain a valid certificate chain and private
  key; both are loaded and validated during startup.
- `min_version` enforces the minimum negotiated TLS version (`1.2` or `1.3`)
  on both listeners.
- `mutual_tls` requires clients to present a certificate signed by a CA in
  `client_ca_file` on both listeners.
- With `tls.enabled: false` (the bundled default), both listeners serve
  plaintext; terminate TLS at a trusted proxy or ingress in that mode.

## Reliability Modes

The collector currently supports:

- `direct`
- `spool`
- `queue`

Do not assume `hybrid` mode is available in the current release.

## Compression

Current ingest support:

- gzip-compressed request bodies

Do not assume zstd or compressed responses are available in the current release.

## Configuration Reload

The current release should be operated with restart-based config changes.

Do not assume SIGHUP hot reload is available.
