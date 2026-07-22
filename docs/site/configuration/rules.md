# Detection Rules

## Static Rules

Fixed threshold comparisons. Fire when a metric crosses a known limit.

```yaml
detection:
  static_rules:
    - name: high_cpu_ratio          # Unique rule name
      query: |                      # PromQL query
        max(rate(container_cpu_usage_seconds_total{...}[1m])) by (cluster, namespace, pod)
        / max(kube_pod_container_resource_limits{resource="cpu",...}) by (cluster, namespace, pod)
      threshold: 0.9                # Comparison value
      operator: ">"                 # Operator: >, <, >=, <=
      severity: warning             # warning or critical
```

### Default Static Rules

| Name | What it detects | Threshold |
|------|----------------|-----------|
| `high_cpu_ratio` | CPU usage > 90% of limit | 0.9 |
| `high_restart_rate` | > 3 restarts in 5 minutes | 3 |
| `high_memory_ratio` | Memory usage > 85% of limit | 0.85 |

> This deployment queries a federated Prometheus spanning multiple
> Kubernetes clusters (`cluster` label). Every rule's `by()` includes
> `cluster` — see [Writing Custom Rules](#writing-custom-rules) below.

---

## Adaptive Metrics

Metrics monitored with adaptive Z-Score detection. No fixed threshold — the system learns what's normal.

```yaml
detection:
  adaptive_metrics:
    - name: cpu_by_workload         # Unique name
      query: |                      # PromQL query
        max(rate(container_cpu_usage_seconds_total{...}[2m])) by (cluster, namespace, pod)
      group_by: [cluster, namespace, pod]   # Labels that identify a unique series
      direction: up_bad             # optional: up_bad | down_bad | both_bad (default)
      min_value: 20                 # optional: absolute floor the reading must cross
```

!!! note "`rate()` windows"
    Use `[2m]` (not `[1m]`) — the org scrape interval is 30s and `rate()` needs ≥4× the
    scrape interval to survive a missed scrape.

### `direction` — direction-of-badness

The Z-Score fires symmetrically on `|z|`. Set `direction` so a rule only fires when the
metric moves the **bad** way, dropping the false positives from the harmless direction
(e.g. latency *improving*):

| Value | Fires when | Use for |
|-------|-----------|---------|
| `up_bad` | value rises above baseline | latency, error rate, queue depth, GC heap, throttling |
| `down_bad` | value falls below baseline | ready replicas, available capacity |
| `both_bad` (or empty) | any deviation | request/traffic rate (a drop can be an outage) |

Filtering runs in the controller before FDR; drops are counted by
`staffops_ad_detection_direction_filtered_total`.

### `min_value` — absolute floor

The Z-Score is **scale-free**: it answers "is this reading unusual for this series?", never
"is this reading large enough to matter?". A gauge that idles near zero therefore has a tiny
stddev, so any reading of a few units is a large `z` — statistically anomalous, operationally
noise.

This was the dominant false-positive source in homolog: `http_client_active_requests`
alone produced **62% of all fired alerts**, with baselines around `0.08`–`0.30` and firings
like `0.0846 → 2` (6.1σ) or `0.2973 → 46` (13.9σ). Note these are *high*-z firings — raising
the z-threshold does not help; the readings are genuinely unusual, just irrelevant.

`min_value` adds an operational-magnitude gate on top of the statistical one. The rule fires
only when the deviation is **both** significant (`|z| > threshold`) **and** the reading crosses
the floor:

```yaml
- name: http_client_active_requests
  query: max(http_client_active_requests) by (cluster, namespace, pod)
  direction: up_bad
  min_value: 20        # ignore a quiet pod going from 0.08 to 2 in-flight requests
```

- Compared against the **absolute** reading (`|value|`), so it is meaningful in either direction.
- Omitted or `0` = no floor (the default; fully backward-compatible).
- Applies to adaptive rules only — static and log-pattern detections are unaffected.
- Runs in the controller **before** FDR, so floored firings do not consume BH acceptance.
  Drops are counted by `staffops_ad_detection_floor_filtered_total`.

Prefer `min_value` over converting the rule to a static threshold: the floor keeps the
per-series adaptivity (each service still learns its own normal) while suppressing the
firings that are too small to act on.

### Default Adaptive Metrics

| Name | Signal | Group by | Direction |
|------|--------|----------|-----------|
| `error_rate_by_service` | Error rate (span metrics) | cluster, service_name | up_bad |
| `request_rate_by_service` | Request rate (span metrics) | cluster, service_name | both_bad |
| `latency_p99_by_service` | P99 latency (span metrics) | cluster, service_name | up_bad |
| `http_error_ratio_by_service` | Unbiased 5xx ratio (OTel SDK) | cluster, service_name | up_bad |
| `http_latency_p99_by_service` | Unbiased p99 latency (OTel SDK) | cluster, service_name | up_bad |
| `db_latency_p99_by_service` | DB op latency (OTel SDK) | cluster, service_name | up_bad |
| `cpu_throttling_ratio` | CFS throttled/total periods | cluster, namespace, pod | up_bad |

The tuned deployed set (per-cluster via the Helm `detection:` override) is larger — see the
chart values and the program map for the full list.

---

## Log Patterns

Loki-based detection rules. Two types:

### Rate-based (adaptive)

```yaml
detection:
  log_patterns:
    - name: error_rate_by_namespace
      query: sum(rate({service_namespace=~".+"} |= "error" [1m])) by (cluster, service_namespace)
      group_by: [cluster, service_namespace]
```

### Pattern matching (immediate)

```yaml
    - name: panic_oom
      query: '{service_namespace=~".+"} |~ "(?i)(panic|oom|out of memory|fatal)"'
      type: pattern_match
```

### Default Log Patterns

| Name | Type | Signal |
|------|------|--------|
| `error_rate_by_namespace` | Rate | Error log volume per namespace |
| `log_volume_by_workload` | Rate | Total log volume per workload |
| `panic_oom` | Pattern | Panic/OOM/fatal messages |

---

## Event Patterns

K8s event reasons that trigger detection:

```yaml
detection:
  event_patterns:
    - CrashLoopBackOff
    - OOMKilled
    - Evicted
    - FailedScheduling
    - BackOff
```

---

## Writing Custom Rules

### Guidelines

1. **Always include `cluster`** in every `by()`/`group_by` — this deployment
   queries a federated multi-cluster Prometheus/Loki. Omitting `cluster`
   silently collapses every cluster's series into one, breaks per-cluster
   baselines, and (for workload-pattern detection) can incorrectly correlate
   identically-named pods/namespaces across different clusters as the same
   workload.
2. **Use `group_by`** to identify unique series — this determines baseline granularity
3. **Exclude noisy namespaces** in the query itself using `namespace!~"..."` or via suppression config
4. **Test with replay** before deploying: `controller --replay --from=24h --config=new.yaml`

### Example: Custom Adaptive Rule

```yaml
- name: disk_io_by_node
  query: rate(node_disk_io_time_seconds_total[5m])
  group_by: [cluster, instance, device]
```

### Example: Custom Static Rule

```yaml
- name: pending_pods
  query: count(kube_pod_status_phase{phase="Pending"}) by (cluster, namespace)
  threshold: 5
  operator: ">"
  severity: warning
```
