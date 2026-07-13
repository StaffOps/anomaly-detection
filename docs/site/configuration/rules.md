# Detection Rules

## Static Rules

Fixed threshold comparisons. Fire when a metric crosses a known limit.

```yaml
detection:
  static_rules:
    - name: high_cpu_ratio          # Unique rule name
      query: |                      # PromQL query
        max(rate(container_cpu_usage_seconds_total{...}[1m])) by (namespace, pod)
        / max(kube_pod_container_resource_limits{resource="cpu",...}) by (namespace, pod)
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

---

## Adaptive Metrics

Metrics monitored with adaptive Z-Score detection. No fixed threshold — the system learns what's normal.

```yaml
detection:
  adaptive_metrics:
    - name: cpu_by_workload         # Unique name
      query: |                      # PromQL query
        max(rate(container_cpu_usage_seconds_total{...}[1m])) by (namespace, pod)
      group_by: [namespace, pod]    # Labels that identify a unique series
```

### Default Adaptive Metrics

| Name | Signal | Group by |
|------|--------|----------|
| `cpu_by_workload` | CPU usage rate per pod | namespace, pod |
| `error_rate_by_service` | Error rate from span metrics | service_name |
| `request_rate_by_service` | Request rate from span metrics | service_name |
| `latency_p99_by_service` | P99 latency from span metrics | service_name |

---

## Log Patterns

Loki-based detection rules. Two types:

### Rate-based (adaptive)

```yaml
detection:
  log_patterns:
    - name: error_rate_by_namespace
      query: sum(rate({service_namespace=~".+"} |= "error" [1m])) by (service_namespace)
      group_by: [service_namespace]
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

1. **Use `group_by`** to identify unique series — this determines baseline granularity
2. **Exclude noisy namespaces** in the query itself using `namespace!~"..."` or via suppression config
3. **Test with replay** before deploying: `controller --replay --from=24h --config=new.yaml`

### Example: Custom Adaptive Rule

```yaml
- name: disk_io_by_node
  query: rate(node_disk_io_time_seconds_total[5m])
  group_by: [instance, device]
```

### Example: Custom Static Rule

```yaml
- name: pending_pods
  query: count(kube_pod_status_phase{phase="Pending"}) by (namespace)
  threshold: 5
  operator: ">"
  severity: warning
```
