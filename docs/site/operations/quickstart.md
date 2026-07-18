# Quick Start

## Prerequisites

- Docker and Docker Compose
- Access to Prometheus, Loki, and Alertmanager endpoints
- Environment variables configured (see below)

## 1. Configure Environment

Copy the example env file and fill in your endpoints:

```bash
cp .env.example scripts/.env
```

Edit `scripts/.env`:

```bash
# Required
PROMETHEUS_URL=https://prometheus-read.example.com/select/0/prometheus
LOKI_URL=https://loki.example.com
ALERTMANAGER_URL=https://alertmanager.example.com

# Optional
CLUSTER_NAME=my-cluster
ML_ENABLED=true
EXCLUDE_NAMESPACES_CSV=kube-system,kube-node-lease
```

## 2. Start the Stack

```bash
./scripts/start.sh
```

This builds Go binaries and starts:

- 1× Controller (port 8080 for metrics)
- 3× Workers (gRPC port 50052)
- 1× Redis (port 6379)
- 1× ML Service (gRPC port 50051, metrics port 8082)

## 3. Monitor

=== "Overview"
    ```bash
    ./scripts/monitor.sh
    ```
    Shows: cycle count, anomalies, alerts fired, worker status.

=== "Controller Detail"
    ```bash
    ./scripts/monitor-controller.sh
    ```
    Shows: anomalies by detector, dedup hits, correlation, enrichment.

=== "Worker Detail"
    ```bash
    ./scripts/monitor-workers.sh
    ```
    Shows: queries executed, baseline series tracked, errors.

=== "Logs"
    ```bash
    docker compose -f scripts/docker-compose.yaml logs -f controller
    ```

## 4. Stop

```bash
./scripts/stop.sh
```

## 5. Verify It's Working

After starting, check:

```bash
# Health check
curl -s http://localhost:8080/readyz
# Expected: 200 OK (or 503 if a dependency is unreachable)

# Metrics
curl -s http://localhost:8080/metrics | grep staffops_ad_controller_cycle
# Expected: cycle_duration_seconds histogram with increasing count
```

!!! success "Healthy indicators"
    - `staffops_ad_controller_cycle_duration_seconds_count` increasing every 30s
    - `staffops_ad_worker_queries_total` increasing
    - `staffops_ad_detection_anomalies_total` > 0 (some anomalies expected)
    - Logs show `[DRY-RUN] would fire alert` messages

!!! warning "Common issues"
    - **503 on /readyz**: Check that PROMETHEUS_URL, LOKI_URL, ALERTMANAGER_URL are reachable from inside Docker
    - **No anomalies**: Wait 30+ minutes for baselines to warm up (60 samples × 30s)
    - **ML errors**: Check ML service logs: `docker compose -f scripts/docker-compose.yaml logs ml`
