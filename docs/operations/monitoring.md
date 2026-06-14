# Monitoring

## Metrics

All metrics use the `staffops_ad_` prefix with 5 sub-namespaces.

### Controller Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_controller_cycle_duration_seconds` | Histogram | Detection cycle duration |
| `staffops_ad_controller_workers_available` | Gauge | Number of healthy workers |
| `staffops_ad_controller_readiness_checks_total` | Counter | Readiness probe results by dependency |
| `staffops_ad_controller_build_info` | Gauge | Version and cluster info |

### Worker Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_worker_queries_total` | Counter | Queries executed by status |
| `staffops_ad_worker_query_duration_seconds` | Histogram | Query latency |
| `staffops_ad_worker_baseline_series_tracked` | Gauge | Number of active baseline series |

### Detection Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_detection_anomalies_total` | Counter | Anomalies by detector, severity, signal |
| `staffops_ad_detection_anomalies_by_workload_total` | Counter | Anomalies sliced by namespace+workload (bounded for dashboards) |
| `staffops_ad_detection_suppressed_total` | Counter | Suppressed anomalies |
| `staffops_ad_detection_workload_patterns_total` | Counter | Workload-level patterns detected |
| `staffops_ad_detection_pod_alerts_suppressed_total` | Counter | Pod alerts suppressed by workload pattern |

### Alert Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_alert_alerts_fired_total` | Counter | Alerts dispatched by severity |
| `staffops_ad_alert_dedup_hits_total` | Counter | Dedup cooldown hits |
| `staffops_ad_alert_enrichment_runs_total` | Counter | Enrichment executions by kind |
| `staffops_ad_alert_enrichment_cache_hits_total` | Counter | Enrichment cache hits |

### ML Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_ml_calls_total` | Counter | ML service calls by method and status |
| `staffops_ad_ml_call_duration_seconds` | Histogram | ML call latency |
| `staffops_ad_ml_multivariate_anomalies_total` | Counter | ML-confirmed anomalies |

---

## Health Endpoints

### `/readyz`

Returns 200 if all dependencies are reachable, 503 otherwise.

Probes:

| Dependency | Check | Timeout |
|------------|-------|---------|
| Redis | `PING` | 3s |
| VictoriaMetrics | `query=up` | 3s |
| Loki | `/loki/api/v1/labels` | 3s |
| Alertmanager | `/api/v2/status` | 3s |
| ML Service | gRPC Health (no-op if disabled) | 3s |

### `/metrics`

Prometheus-format metrics endpoint on port 8080.

---

## Scrape Configuration

### Local Development (Prometheus)

```yaml
scrape_configs:
  - job_name: staffops-ad-controller
    static_configs:
      - targets: ['controller:8080']
        labels:
          component: controller
          cluster: local

  - job_name: staffops-ad-ml
    static_configs:
      - targets: ['ml:8082']
        labels:
          component: ml
          cluster: local
```

### Production (vmagent)

```yaml
# vmagent externalLabels adds cluster/environment context
# App only emits service identity (cluster label from CLUSTER_NAME env var)
```

---

## Key Dashboards (Planned)

| Panel | Query | Purpose |
|-------|-------|---------|
| Detection volume | `rate(staffops_ad_detection_anomalies_total[5m])` | Are we detecting? |
| Top noisy workloads | `topk(20, staffops:detection_anomalies_24h:by_workload)` | What to suppress? (uses recording rule) |
| Cardinality watch | `count by (__name__) ({__name__=~"staffops_ad_.+"})` | Cardinality safety |
| Cycle health | `histogram_quantile(0.99, ...)` | Is the cycle keeping up? |
| ML effectiveness | `rate(staffops_ad_ml_multivariate_anomalies_total[1h])` | Is ML adding value? |
| Dedup ratio | `rate(dedup_hits[5m]) / rate(alerts_fired[5m])` | Is dedup working? |

## Recording Rules

VMRule `staffops-ad-recording` in `controller/deploy/vmrules.yaml` pre-aggregates expensive queries:

| Recording rule | Window | Purpose |
|----------------|--------|---------|
| `staffops:detection_anomalies_24h:by_workload` | 24h | Top noisy workloads panel |
| `staffops:detection_anomalies_24h:by_workload_severity` | 24h | Stacked severity breakdown |
| `staffops:detection_anomalies_1h:by_workload` | 1h | "Currently noisy" detection |

These are evaluated every 1 minute. Dashboards query the recording rules instead of computing `increase(...[24h])` on every render.

## Health Alerts

VMRule `staffops-ad-health` in `controller/deploy/vmrules.yaml`:

| Alert | Severity | Trigger |
|-------|----------|---------|
| `StaffOpsADNoLeader` | critical | No active controller for 2min |
| `StaffOpsADStalled` | critical | No detection cycles for 5min |
| `StaffOpsADWorkersDown` | critical | No reachable workers for 1min |
| `StaffOpsADHighJobErrorRate` | warning | >50% job failures for 5min |
| `StaffOpsADRedisErrors` | warning | Persistent Redis errors |
| `StaffOpsADCycleSlow` | warning | Cycle p99 > 30s for 10min |
| `StaffOpsADMLErrorRate` | warning | ML errors > 10% for 10min |
| `StaffOpsADWorkloadChronicallyNoisy` | info | Workload >100 anomalies in 24h |

---

## TUI Monitors

The `scripts/monitor*.sh` scripts provide terminal-based dashboards using `watch` + `curl` + `jq`:

| Script | Shows |
|--------|-------|
| `monitor.sh` | Overview: cycles, anomalies, alerts, workers |
| `monitor-controller.sh` | Controller detail: correlation, enrichment, ML |
| `monitor-workers.sh` | Worker detail: queries, baselines, errors |
| `monitor-detail.sh` | Full detail: all metrics parsed |
