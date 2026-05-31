# Scripts

Operational scripts and compose files for the anomaly detection stack. All scripts auto-resolve paths — run from anywhere.

## Files

| File | Purpose |
|------|---------|
| `docker-compose.yaml` | Full stack (controller + workers + redis + ML + local observability) — external endpoints |

## Docker Compose Mode (external endpoints)

| Script | Purpose |
|--------|---------|
| `start.sh` | Build Go binaries + start full stack |
| `stop.sh` | Stop the stack |
| `monitor.sh` | TUI overview — cycles, anomalies, alerts, dedup stats |
| `monitor-controller.sh` | Controller-focused TUI — anomalies, correlations, severity |
| `monitor-workers.sh` | Workers-focused TUI — jobs, queries, baseline learning |
| `monitor-detail.sh` | Detailed anomaly view — per-metric breakdown |

## Usage

```bash
# From repo root
./scripts/start.sh
./scripts/monitor.sh
./scripts/stop.sh

# Or from anywhere
/path/to/repo/scripts/start.sh
```

## Requirements

- Docker + Docker Compose
- `curl` (monitors query Prometheus metrics endpoints)
