# Scripts

Operational scripts and compose files for the anomaly detection stack. All scripts auto-resolve paths — run from anywhere.

## Files

| File | Purpose |
|------|---------|
| `docker-compose.yaml` | Full stack (controller + workers + redis + ML) — external endpoints |
| `docker-compose.local.yaml` | Stack for local mode (uses port-forwards to cluster) |
| `config.local.yaml` | Config for local mode (localhost endpoints) |

## Docker Compose Mode (external endpoints)

| Script | Purpose |
|--------|---------|
| `start.sh` | Build Go binaries + start full stack |
| `stop.sh` | Stop the stack |
| `monitor.sh` | TUI overview — cycles, anomalies, alerts, dedup stats |
| `monitor-controller.sh` | Controller-focused TUI — anomalies, correlations, severity |
| `monitor-workers.sh` | Workers-focused TUI — jobs, queries, baseline learning |
| `monitor-detail.sh` | Detailed anomaly view — per-metric breakdown |

## Local Mode (port-forward to cluster)

| Script | Purpose |
|--------|---------|
| `start-local.sh` | Build binaries + setup port-forwards + start stack |
| `stop-local.sh` | Stop stack + kill port-forwards |
| `monitor-local.sh` | TUI monitor for local mode |

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
- `kubectl` + cluster access (local mode only)
- `curl` (monitors query Prometheus metrics endpoints)
