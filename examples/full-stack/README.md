# Full Stack Example

Complete observability stack: LOZA Collector + Prometheus + Grafana.

## Components

| Component | Port | Description |
|-----------|------|-------------|
| Collector | 9308 | Event ingestion and storage |
| Prometheus | 9091 | Metrics collection |
| Grafana | 3000 | Dashboards and visualization |

## Usage

```bash
docker compose up -d
```

## Verify

```bash
# Collector health
curl http://localhost:9308/health

# Collector metrics (Prometheus exposition)
curl http://localhost:9308/metrics

# Grafana dashboards
open http://localhost:3000
```
