# Metrics Reference

All three components — Controller, Workers, and the ML Service — expose Prometheus-compatible metrics on their HTTP `/metrics` endpoints (port `8080` for Go components, port `8082` for the ML service). All three route through the org's OTel Metrics API helper library (`staffops-otel-libs`: `otelhelper.MetricsHandler()` in Go, `otel_helper`'s Prometheus reader in Python) — no direct `client_golang`/`prometheus_client` usage. The `cluster` (and any other org-specific) label is **not** emitted by the app; it's added at the scrape layer via the ServiceMonitor's `externalLabels` (vmagent) or your Prometheus `external_labels`/relabel config. Queries below assume that label is present from the scrape layer.

---

## Scraping configuration

The chart exposes a single, vendor-neutral scrape mechanism — `ServiceMonitor`. vm-operator
honors the `ServiceMonitor` CRD natively on Prometheus clusters, so there is no separate
`ServiceMonitor` resource to configure.

=== "ServiceMonitor (Prometheus Operator / vm-operator)"

    ```yaml
    serviceMonitor:
      enabled: true
      interval: 30s
      scrapeTimeout: 10s
      labels:
        release: kube-prometheus-stack   # must match your Prometheus selector
    ```

    The chart creates a `ServiceMonitor` resource:

    ```yaml
    apiVersion: monitoring.coreos.com/v1
    kind: ServiceMonitor
    metadata:
      name: staffops-anomaly-detection
      namespace: monitoring
      labels:
        release: kube-prometheus-stack
    spec:
      selector:
        matchLabels:
          app.kubernetes.io/name: staffops-anomaly-detection
      endpoints:
        - port: metrics
          interval: 30s
          scrapeTimeout: 10s
    ```

=== "Manual port-forward (debug)"

    ```bash
    # Controller metrics
    kubectl port-forward -n monitoring deploy/<release>-controller 8080:8080 &
    curl -s localhost:8080/metrics

    # Worker metrics (target a specific pod)
    kubectl port-forward -n monitoring pod/<release>-worker-xyz-abc 8080:8080 &
    curl -s localhost:8080/metrics

    # ML service metrics
    kubectl port-forward -n monitoring deploy/<release>-ml 8000:8000 &
    curl -s localhost:8000/metrics
    ```

---

## Controller metrics

The controller exposes metrics about detection cycle orchestration, leader election state, and alert output. These are the primary health indicators for the system.

### `staffops_ad_controller_cycles_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `result` |
| Description | Total number of completed detection cycles since startup. The `result` label is either `success` or `error`. A flatlined counter on the leader pod indicates the detection loop has stalled. |

**PromQL examples:**

```promql
# Cycle success rate over the last 5 minutes
rate(staffops_ad_controller_cycles_total{result="success"}[5m])

# Error ratio (alert when above 5%)
rate(staffops_ad_controller_cycles_total{result="error"}[5m])
  /
rate(staffops_ad_controller_cycles_total[5m])
  > 0.05
```

---

### `staffops_ad_controller_cycle_duration_seconds`

| Property | Value |
|---|---|
| Type | Histogram |
| Labels | (none) |
| Buckets | `0.1, 0.5, 1, 2, 5, 10, 30` seconds |
| Description | End-to-end duration of each detection cycle: from job dispatch to alert firing. High values indicate slow worker responses or a large number of detection rules. |

```promql
# p99 cycle duration
histogram_quantile(0.99, rate(staffops_ad_controller_cycle_duration_seconds_bucket[10m]))

# Cycles taking more than 25 seconds (approaching the 30s tick interval)
histogram_quantile(0.95, rate(staffops_ad_controller_cycle_duration_seconds_bucket[10m])) > 25
```

---

### `staffops_ad_controller_is_leader`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | `pod` |
| Description | Set to `1` on the pod currently holding the Kubernetes Lease, and `0` on all standbys. Exactly one pod should show `1` at any time. If zero pods show `1`, no detection is running. |

```promql
# Detect no-leader condition
sum(staffops_ad_controller_is_leader) == 0

# Which pod is the leader?
staffops_ad_controller_is_leader == 1
```

---

### `staffops_ad_controller_anomalies_detected_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `source`, `algorithm`, `severity` |
| Description | Total anomalies detected before deduplication. `source` is `metrics` or `logs`. `algorithm` is one of `static`, `zscore`, `ewma`, `isolation_forest`, `prophet`. Useful for understanding which algorithms contribute the most signals. |

```promql
# Anomalies per algorithm over the last hour
sum by (algorithm) (
  increase(staffops_ad_controller_anomalies_detected_total[1h])
)

# Ratio of ML-detected anomalies vs rule-based
sum(increase(staffops_ad_controller_anomalies_detected_total{algorithm=~"isolation_forest|prophet"}[1h]))
  /
sum(increase(staffops_ad_controller_anomalies_detected_total[1h]))
```

---

### `staffops_ad_controller_alerts_fired_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `severity`, `dry_run` |
| Description | Total alerts sent (or would-have-been-sent) to Alertmanager. When `controller.dryRun=true`, the counter still increments but `dry_run="true"` in the label — the Alertmanager is not actually called. Use this to validate rule coverage before enabling live alerting. |

```promql
# Alert firing rate (last 15 minutes)
rate(staffops_ad_controller_alerts_fired_total{dry_run="false"}[15m])

# Compare dry-run vs live rate after enabling alerting
rate(staffops_ad_controller_alerts_fired_total[15m])
```

---

### `staffops_ad_controller_suppressed_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `reason` |
| Description | Anomalies that were detected but suppressed before reaching Alertmanager. `reason` is one of `dedup_ttl` (same anomaly fired recently), `excluded_namespace`, or `static_excluded_namespace`. |

```promql
# Suppression breakdown
sum by (reason) (rate(staffops_ad_controller_suppressed_total[10m]))
```

---

### `staffops_ad_detection_anomalies_by_workload_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `cluster`, `namespace`, `workload`, `severity` |
| Description | Anomalies sliced by cluster/namespace/workload — bounded labels for "top noisy workloads" dashboards, suppression tuning, and drift detection. `workload` is extracted from pod names (`correlation.ExtractWorkload`), never a raw pod ID; service-level anomalies use `service_name` as workload. `cluster` reflects the *monitored* workload's own cluster (this deployment queries a federated multi-cluster Prometheus/Loki), not necessarily the cluster the controller itself runs in. Any of the three identity labels default to `"unknown"` when absent from the source query's `by()`/`group_by`. |

```promql
# Top 10 noisiest workloads across all clusters, last 24h
topk(10, sum by (cluster, namespace, workload) (
  increase(staffops_ad_detection_anomalies_by_workload_total[24h])
))

# Confirm anomalies are attributed to real clusters, not just one
count by (cluster) (staffops_ad_detection_anomalies_by_workload_total)
```

### `staffops_ad_detection_fdr_accepted_total` / `_fdr_rejected_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | none |
| Description | Adaptive anomalies accepted / rejected by the per-cycle Benjamini-Hochberg False Discovery Rate filter (`controller.fdr_target`, default 0.05). Only `detector="adaptive"` anomalies enter the FDR family; static and pattern anomalies always pass through. |

### `staffops_ad_detection_fdr_family_size`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | none |
| Description | The BH family size `m` used in the last detection cycle — the number of adaptive evaluations performed by workers (past warm-up), fired or not, reported via `JobResults.adaptive_series_tested`. This is the count FDR corrects over. A value **near 0 while anomalies are firing** means workers are not reporting tested series and the filter is falling back to the censored (fired-only) family, where BH rejects almost nothing — the exact defect F0 fixed. Expect it to track the number of adaptive series in `config.yaml` (order of hundreds). |

```promql
# FDR rejection ratio — how much multiple-comparison noise is being cut
sum(rate(staffops_ad_detection_fdr_rejected_total[1h]))
  / clamp_min(sum(rate(staffops_ad_detection_fdr_accepted_total[1h]))
    + sum(rate(staffops_ad_detection_fdr_rejected_total[1h])), 1)

# Alert if the family collapses (workers not reporting tested series)
staffops_ad_detection_fdr_family_size < 10
```

### `staffops_ad_detection_direction_filtered_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | none |
| Description | Adaptive anomalies dropped because they deviated in the *harmless* direction for their rule (direction-of-badness). A rule declares `direction: up_bad \| down_bad \| both_bad` (empty = `both_bad`); the controller drops firings that run the other way (e.g. latency *improving*) before FDR. A rising counter means the symmetric z-score was catching one-sided-metric noise that this now suppresses. |

### `staffops_ad_detection_floor_filtered_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | none |
| Description | Adaptive anomalies dropped because the current reading did not cross the rule's `min_value` floor. The z-score is scale-free, so a gauge idling near zero yields a large `z` for a reading of a few units — statistically anomalous, operationally noise. The floor is applied in the controller before FDR, so floored firings do not consume BH acceptance. Rules without `min_value` never contribute here. |

```promql
# Share of adaptive firings suppressed by the floor (last 1h)
sum(rate(staffops_ad_detection_floor_filtered_total[1h]))
  / clamp_min(sum(rate(staffops_ad_detection_floor_filtered_total[1h]))
    + sum(rate(staffops_ad_detection_fdr_accepted_total[1h])), 1)
```

A floor that suppresses ~everything means `min_value` is set too high for that rule — the
detector has gone silent rather than quiet. Compare against `staffops_ad_alert_fired_total`
before and after a floor change.

---

## Worker metrics

Workers expose metrics about query execution, algorithm performance, and Redis baseline operations. Each worker pod exposes metrics independently; aggregate with `sum by (cluster)` for cluster-level views.

### `staffops_ad_worker_jobs_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `pod`, `result` |
| Description | Total detection jobs processed by this worker. `result` is `success` or `error`. A high error rate usually indicates connectivity issues with Prometheus, Loki, or Redis. |

```promql
# Total job throughput across all workers
sum(rate(staffops_ad_worker_jobs_total{result="success"}[5m]))

# Per-worker error rate
rate(staffops_ad_worker_jobs_total{result="error"}[5m])
```

---

### `staffops_ad_worker_anomalies_suppressed_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `pod`, `detector`, `reason` |
| Description | Anomalies dropped by the suppression filter before reaching the controller. `reason` is `namespace_all` (namespace fully excluded), `namespace_static` (static-only namespace), or `adaptive_workload` (workload on the adaptive exclude list). Pair with `staffops_ad_worker_detections_total` (pre-suppression) to see the suppression rate. See [Suppression](../configuration/suppression.md). |

```promql
# Suppression rate by reason
sum by (reason) (rate(staffops_ad_worker_anomalies_suppressed_total[10m]))

# Fraction of adaptive detections silenced by the workload exclude list
sum(rate(staffops_ad_worker_anomalies_suppressed_total{reason="adaptive_workload"}[10m]))
  / sum(rate(staffops_ad_worker_detections_total{detector="adaptive"}[10m]))
```

---

### `staffops_ad_worker_query_duration_seconds`

| Property | Value |
|---|---|
| Type | Histogram |
| Labels | `pod`, `datasource` |
| Buckets | `0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10` seconds |
| Description | Duration of individual queries to each datasource. `datasource` is `victoriametrics` or `loki`. Slow queries (p99 > 5s) directly lengthen detection cycles and may indicate datasource pressure or overly complex PromQL expressions. |

```promql
# p99 query latency per datasource
histogram_quantile(0.99,
  sum by (datasource, le) (
    rate(staffops_ad_worker_query_duration_seconds_bucket[5m])
  )
)

# Slow query alert: p95 Prometheus latency above 3 seconds
histogram_quantile(0.95,
  sum by (le) (
    rate(staffops_ad_worker_query_duration_seconds_bucket{datasource="victoriametrics"}[5m])
  )
) > 3
```

---

### `staffops_ad_worker_redis_cache_hits_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `pod`, `operation` |
| Description | Redis cache hits during baseline lookups. `operation` is `baseline_read`, `seasonal_profile_read`, or `dedup_check`. A hit rate below 80% on `baseline_read` suggests baselines are expiring too quickly — consider increasing `redis.ttl.baseline`. |

```promql
# Cache hit ratio for baseline reads (last 10 minutes)
sum(rate(staffops_ad_worker_redis_cache_hits_total{operation="baseline_read"}[10m]))
  /
sum(rate(staffops_ad_worker_redis_operations_total{operation="baseline_read"}[10m]))
```

---

### `staffops_ad_worker_redis_operations_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `pod`, `operation`, `result` |
| Description | Total Redis operations performed. `operation` matches the cache hits metric. `result` is `hit`, `miss`, or `error`. Use alongside `redis_cache_hits_total` to compute hit ratios. |

```promql
# Redis error rate — may indicate connectivity or auth problems
rate(staffops_ad_worker_redis_operations_total{result="error"}[5m])
```

---

### `staffops_ad_worker_baseline_warmup_remaining`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | `pod`, `rule` |
| Description | Number of samples still needed before adaptive detection activates for a given rule. Set to `0` once a rule's baseline has warmed up. Monitoring this gauge after install or after a Redis restart shows warm-up progress. |

```promql
# Count of rules still in warm-up across all workers
count(staffops_ad_worker_baseline_warmup_remaining > 0)

# Which specific rules are still warming up?
staffops_ad_worker_baseline_warmup_remaining > 0
```

---

### `staffops_ad_worker_baseline_series_tracked`

| Property | Value |
|---|---|
| Type | Gauge |
| Description | Count of distinct baseline series (metric+label combinations) evaluated by this worker process since startup — cardinality watch for the EWMA baseline store. Per-instance; `sum()` across workers for the cluster-wide total. Resets on pod restart. |

```promql
# Total distinct baseline series tracked across all workers
sum(staffops_ad_worker_baseline_series_tracked)
```

---

### `staffops_ad_worker_zscore_current`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | `pod`, `rule`, `series_hash` |
| Description | Current Z-Score value for each evaluated series. Useful for dashboarding — shows how far each series is from its baseline at the time of last evaluation. Values beyond ±3 indicate anomalous behavior (with default threshold). |

```promql
# Top 10 highest Z-Scores right now (potential anomalies)
topk(10, abs(staffops_ad_worker_zscore_current))
```

---

## ML Service metrics

The ML service exposes metrics about inference requests, model performance, and retraining operations. The ML service metrics endpoint runs on port `8000`.

### `staffops_ad_ml_predictions_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `model`, `result` |
| Description | Total inference requests served. `model` is `isolation_forest` or `prophet`. `result` is `anomaly`, `normal`, or `error`. Track the `anomaly` rate to understand ML detection volume relative to total predictions. |

```promql
# Anomaly detection rate by model
sum by (model) (
  rate(staffops_ad_ml_predictions_total{result="anomaly"}[10m])
)

# ML inference error rate
rate(staffops_ad_ml_predictions_total{result="error"}[5m])
```

---

### `staffops_ad_ml_inference_duration_seconds`

| Property | Value |
|---|---|
| Type | Histogram |
| Labels | `model` |
| Buckets | `0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5` seconds |
| Description | Time spent on each inference call, from request received to response sent, per model. Isolation Forest inference is typically fast (<50ms); Prophet can be slower (100–500ms) depending on forecast horizon and dataset size. |

```promql
# p99 inference latency per model
histogram_quantile(0.99,
  sum by (model, le) (
    rate(staffops_ad_ml_inference_duration_seconds_bucket[5m])
  )
)

# Alert if Isolation Forest p95 exceeds 200ms
histogram_quantile(0.95,
  sum by (le) (
    rate(staffops_ad_ml_inference_duration_seconds_bucket{model="isolation_forest"}[5m])
  )
) > 0.2
```

---

### `staffops_ad_ml_retraining_total`

| Property | Value |
|---|---|
| Type | Counter |
| Labels | `model`, `result` |
| Description | Total model retraining runs completed. `result` is `success` or `error`. A retraining error means the previous model version remains in use until the next successful retrain. |

```promql
# Retraining success rate
rate(staffops_ad_ml_retraining_total{result="success"}[1h])

# Detect consecutive retraining failures (alert if no success in 3× retrain interval)
increase(staffops_ad_ml_retraining_total{result="success"}[3h]) == 0
```

---

### `staffops_ad_ml_retraining_duration_seconds`

| Property | Value |
|---|---|
| Type | Histogram |
| Labels | `model` |
| Buckets | `1, 5, 10, 30, 60, 120, 300` seconds |
| Description | Time taken to complete a full model retraining cycle. Isolation Forest retraining is typically 5–30 seconds; Prophet can take 30–120 seconds depending on the history depth. If retraining exceeds `ml.retrainInterval`, consider increasing the interval or reducing the dataset. |

```promql
# Latest retraining duration per model
histogram_quantile(0.5,
  sum by (model, le) (
    rate(staffops_ad_ml_retraining_duration_seconds_bucket[2h])
  )
)
```

---

### `staffops_ad_ml_model_training_samples`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | `model` |
| Description | Number of training samples used in the most recent retraining run. Prophet requires a minimum of ~336 samples (2 weeks at 30s tick × 5 samples/min aggregated). Low values indicate insufficient historical data. |

```promql
# Check if Prophet has enough training data
staffops_ad_ml_model_training_samples{model="prophet"}

# Alert if Prophet training data is below 2-week minimum
staffops_ad_ml_model_training_samples{model="prophet"} < 336
```

---

### `staffops_ad_ml_anomaly_score`

| Property | Value |
|---|---|
| Type | Gauge |
| Labels | `model`, `rule` |
| Description | Most recent anomaly score returned by the model for each evaluated rule. For Isolation Forest, lower scores (closer to `-1`) indicate more anomalous. For Prophet, the score represents the normalized deviation from the forecast confidence interval. |

```promql
# Rules with the most anomalous Isolation Forest scores right now
sort_desc(staffops_ad_ml_anomaly_score{model="isolation_forest"})

# Prophet deviations above 2× the uncertainty interval
staffops_ad_ml_anomaly_score{model="prophet"} > 2
```
