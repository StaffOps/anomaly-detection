# staffops-anomaly-detection

Distributed anomaly detection system for Kubernetes clusters. Combines adaptive statistical detection (Go) with ML-based forecasting and multivariate analysis (Python).

## Structure

```
staffops-anomaly-detection/
├── scripts/        # Operational scripts, docker-compose, configs
│   ├── docker-compose.yaml       # Full stack + local observability
│   ├── start.sh / stop.sh        # Build + run
│   └── monitor*.sh               # TUI dashboards
├── controller/     # Go — controller + gRPC workers + detection engine
│   ├── cmd/        # Entrypoints (controller, worker)
│   ├── internal/   # Detection, correlation, baselines, ML client, alerts
│   ├── proto/      # Protobuf definitions + generated Go stubs
│   ├── config.yaml # Main config (datasources, rules, suppression)
│   └── deploy/     # K8s manifests
└── ml/             # Python — ML service (Prophet + Isolation Forest)
    ├── server/     # gRPC server (forecaster, multivariate, health)
    ├── proto/      # Protobuf source
    └── Dockerfile
```

## Quick Start

```bash
# Build + start full stack (controller + 3 workers + redis + ML service)
./scripts/start.sh

# Monitor
./scripts/monitor.sh

# Stop
./scripts/stop.sh
```

## Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              Controller (Go)                 │
                    │  ┌─────────┐  ┌──────────┐  ┌───────────┐  │
                    │  │ Scheduler│→│Correlator │→│ Dispatcher │  │
                    │  └────┬────┘  └──────────┘  └───────────┘  │
                    │       │              ↑                       │
                    │       │       ┌──────┴──────┐               │
                    │       │       │  ML Client  │               │
                    │       │       └──────┬──────┘               │
                    └───────┼──────────────┼──────────────────────┘
                            │ gRPC         │ gRPC
                    ┌───────▼───────┐  ┌───▼────────────┐
                    │ Workers (x3)  │  │  ML Service     │
                    │  - VM queries │  │  - Prophet      │
                    │  - Loki queries│  │  - Isolation    │
                    │  - Detection  │  │    Forest       │
                    └───────┬───────┘  └────────────────┘
                            │
                    ┌───────▼───────┐
                    │     Redis     │
                    │  - Baselines  │
                    │  - Dedup TTL  │
                    │  - Seasonal   │
                    └───────────────┘
```

## Components

| Component | Language | Port | Purpose |
|-----------|----------|------|---------|
| Controller | Go | 8080 (metrics) | Schedules detection, correlates, calls ML, fires alerts |
| Worker (x3) | Go | 50052 (gRPC) | Executes queries, runs detection algorithms, updates baselines |
| ML Service | Python | 50051 (gRPC), 8082 (metrics) | Prophet forecasting, Isolation Forest multivariate |
| Redis | — | 6379 | EWMA baselines, dedup cooldowns, seasonal profiles |

## Detection Pipeline

```
Every 30s:
  1. Controller builds job batch (static + adaptive + log rules)
  2. Workers execute queries against VictoriaMetrics/Loki
  3. Workers run detection (static threshold, Z-Score, log rate)
  4. Controller receives anomalies
  5. ML Isolation Forest evaluates multivariate correlation (≥2 anomalies)
  6. Correlator groups by workload, deduplicates, escalates severity
  7. Dispatcher fires to Alertmanager (dry-run mode available)
```

## Detection Methods

| Method | Detector | Trigger |
|--------|----------|---------|
| Static threshold | `static` | Value > configured limit (CPU > 90%, restarts > 3) |
| Adaptive Z-Score | `adaptive` | Value deviates > 3σ from EWMA baseline |
| Log rate anomaly | `adaptive` | Log volume spike via Loki queries |
| ML Multivariate | `ml_isolation_forest` | Correlated anomalies across multiple metrics |
| ML Forecast | `ml_forecast` | Prophet predicts threshold breach within 30min (ready, not yet wired) |

## Configuration

Main config: `controller/config.yaml`

```yaml
datasources:
  victoriametrics:
    url: ${VM_URL}             # e.g. https://victoria-metrics-read.example.com/select/0/prometheus
  loki:
    url: ${LOKI_URL}           # e.g. https://loki.example.com
  alertmanager:
    url: ${ALERTMANAGER_URL}   # e.g. https://alertmanager.example.com

ml:
  endpoint: ml:50051
  enabled: true
  timeout: 5s

# Suppression is org-specific — set via env vars (CSV), never hardcoded.
suppression:
  exclude_namespaces_csv: ${EXCLUDE_NAMESPACES_CSV:kube-system}
  exclude_static_only_csv: ${EXCLUDE_STATIC_ONLY_CSV:}
```

All endpoints, cluster names and namespace lists come from env vars. See `.env.example` for the full list.

## Status

### ✅ Done
- Static threshold detection (CPU, memory, restarts)
- Adaptive Z-Score detection with EWMA baselines (Welford's algorithm)
- Log rate anomaly detection (Loki)
- Correlation engine (dedup via Redis TTL, cooldown, severity escalation)
- Alert dispatcher (Alertmanager integration, dry-run mode)
- Suppression filter (namespace-level, static-only mode)
- Config hot-reload (file watcher)
- ML service: Prophet forecasting (gRPC)
- ML service: Isolation Forest multivariate (gRPC)
- ML client integrated into controller cycle (DetectMultivariate)
- docker-compose stack (controller + 3 workers + redis + ML)
- Monorepo consolidation (controller + ml in single repo)
- Operational scripts (start, stop, monitor TUIs)

### 🚧 In Progress
- [ ] Replay mode for historical data validation — **complete** (offline `--replay` CLI; see `controller/README.md`)

### 🔜 Next
- [ ] Wire ML Forecast (Prophet) into cycle (needs baseline time-series export from Redis)
- [ ] K8s Lease leader election (multi-replica controller HA)
- [ ] Remove `--dry-run` and validate real alerts via Alertmanager → Slack
- [ ] Deploy to cluster (K8s manifests in `deploy/`)
- [ ] Feedback loop (mark false positives to adjust baselines)
- [ ] Agent API integration — invoke staffops-chaitops squad on high-confidence anomalies (spec: `.kiro/specs/agent-api-integration/`)

## Development

```bash
# Build Go binaries
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine sh -c \
  "CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ && \
   CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/"

# Run Go tests
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine go test ./...

# Build ML image
docker build -t staffops-anomaly-ml ./ml

# Run Python tests
docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
  "pip install -e '.[dev]' -q && pytest tests/ -v"
```
