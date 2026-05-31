# staffops-anomaly-detection / controller

Distributed anomaly detection for Kubernetes clusters. Consumes metrics, logs, and events to detect anomalies using adaptive baselines and pattern matching.

## Architecture

```
Controller (active/standby) → gRPC → Workers (3+ stateless) → VM/Loki/K8s Events
                                              ↕
                                           Redis (baseline, dedup)
                                              
ML Service (Python) ← gRPC ← Controller (planned integration)
  ├── Prophet (forecasting)
  └── Isolation Forest (multivariate)
```

- **Controller**: schedules detection jobs, correlates results, fires alerts (`--dry-run` mode available)
- **Workers**: execute queries, run detection algorithms, update baselines
- **Redis**: shared state (baselines, dedup cooldowns, seasonal profiles)
- **ML Service**: Prophet forecasting + Isolation Forest multivariate detection (standalone, ready for integration)

## Detection Methods

| Method | How it works | Use case |
|--------|-------------|----------|
| Static threshold | Value > configured limit | CPU > 90%, restarts > 3 |
| Adaptive (Z-Score) | Value deviates > 3σ from EWMA baseline | Spikes in any metric |
| Seasonal | Compares to same hour/day-of-week historical | Avoids false positives on batch jobs |
| Pattern matching | K8s event reason matching | CrashLoopBackOff, OOMKilled |

## Quick Start

```bash
# Build + start full stack (controller + 3 workers + redis + ML)
../scripts/start.sh

# Monitor
../scripts/monitor.sh              # Overview
../scripts/monitor-controller.sh   # Controller: anomalies, dedup, correlations
../scripts/monitor-workers.sh      # Workers: jobs, queries, baseline learning

# Stop
../scripts/stop.sh

# Logs
docker compose logs -f
docker compose logs controller --tail 50
```

## Configuration

Main config: `config.yaml`

Key settings:
- `datasources.victoriametrics.timeout`: 30s (queries to VM)
- `datasources.loki.timeout`: 30s (queries to Loki)
- `suppression.exclude_static_only`: namespaces where static rules are suppressed (batch/cron workloads)
- `ml.enabled`: true (ML service endpoint)
- `controller.job_interval`: 30s (detection cycle frequency)

See `config/config.local.yaml` for local-only overrides.

## Suppression

Namespaces with known noisy workloads (CronJobs, batch) are suppressed for static detection only — adaptive (Z-Score) still fires if something truly anomalous happens:

```yaml
# Org-specific noisy namespaces are passed via env vars (CSV).
# Never hardcoded in the repo — see `.env.example` at the repo root.
suppression:
  exclude_namespaces_csv: ${EXCLUDE_NAMESPACES_CSV:kube-system}
  exclude_static_only_csv: ${EXCLUDE_STATIC_ONLY_CSV:}
```

Common patterns:
- `EXCLUDE_NAMESPACES_CSV` — namespaces fully ignored (no detection)
- `EXCLUDE_STATIC_ONLY_CSV` — namespaces where static rules are suppressed (typically batch/cron with unpredictable workload), but adaptive still fires

## Multi-cluster labels

The controller emits a single universal constant label `cluster` (from `CLUSTER_NAME` env var, kubernetes-mixin convention). It does **not** emit any organization-specific labels — those belong at the scrape layer.

When you need additional labels (`environment`, `eks_cluster`, `team`, `region`, etc.), configure them at the scrape layer instead of in app code:

| Setup | Where to configure |
|-------|--------------------|
| Production with vmagent | `vmagent` `externalLabels` (preferred — single point of config, applies on remote_write to vmstorage) |
| Production with Prometheus Operator | `Prometheus` CRD `externalLabels` (same caveat as below for queries) |
| Local dev — single Prometheus | `static_configs.labels` per scrape job (visible on local queries) |

> **Gotcha**: top-level `external_labels` only applies on remote_write, federation, and Alertmanager — they are **not** visible on local PromQL queries. For a single-Prom dev stack you must use per-job labels.

Example for local dev — already configured in `scripts/observability/prometheus.yml`:

```yaml
scrape_configs:
  - job_name: staffops-ad-controller
    static_configs:
      - targets: ['controller:8080']
        labels:
          component: controller
          eks_cluster: local
          environment: dev
```

Why this separation: the same app binary runs across environments. The labels that distinguish them are deployment context, not application identity. Keeping the app generic also lets other organizations adopt the project without forking. See `staffops_agent_definition/steering/observability-principles.md`: SDK responsibility is service identity; Collector/scrape layer adds environment metadata.

## Deploy

```bash
kubectl apply -f deploy/
```

## Related

- [ML Service](../ml) — Python ML service (Prophet + Isolation Forest)
- [Scripts](../scripts) — Operational scripts, docker-compose, monitors

## Status

### ✅ Done
- [x] Static threshold detection (CPU, memory, restarts)
- [x] Adaptive Z-Score detection with EWMA baselines
- [x] Log rate anomaly detection (Loki)
- [x] Correlation engine (dedup, cooldown, severity escalation)
- [x] Alert dispatcher (Alertmanager integration, dry-run mode)
- [x] Suppression filter (namespace-level, static-only mode)
- [x] Config hot-reload (watcher)
- [x] ML service: Prophet forecasting functional
- [x] ML service: Isolation Forest multivariate functional
- [x] ML service: Docker build + gRPC health check
- [x] ML client integrated into controller (DetectMultivariate)
- [x] docker-compose stack (controller + workers + redis + ML)

### 🔜 Next
- [ ] Wire ML Forecast (Prophet) — needs time-series export from Redis baselines
- [ ] K8s Lease leader election (multi-replica controller HA)
- [ ] Remove `--dry-run` and validate real alerts via Alertmanager → Slack
- [ ] Deploy to cluster (K8s manifests in `deploy/`)
- [ ] Replay mode for historical data validation
- [ ] Feedback loop (mark false positives to adjust baselines)
