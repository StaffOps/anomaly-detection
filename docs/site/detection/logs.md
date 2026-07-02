# Log Pattern Detection

## How It Works

Queries Loki for log-derived metrics and patterns. Two modes:

1. **Rate-based** — adaptive Z-Score on log volume/error rate (same algorithm as metric detection)
2. **Pattern matching** — fires on specific log content (panic, OOM, fatal)

## Configuration

```yaml
detection:
  log_patterns:
    # Rate-based (uses adaptive Z-Score)
    - name: error_rate_by_namespace
      query: sum(rate({service_namespace=~".+"} |= "error" [1m])) by (service_namespace)
      group_by: [service_namespace]

    - name: log_volume_by_workload
      query: sum(rate({service_namespace=~".+"}[1m])) by (service_namespace, service_workload)
      group_by: [service_namespace, service_workload]

    # Pattern matching (fires immediately on match)
    - name: panic_oom
      query: '{service_namespace=~".+"} |~ "(?i)(panic|oom|out of memory|fatal)"'
      type: pattern_match
```

## Rate-Based Log Detection

Works identically to [adaptive metric detection](adaptive.md) — learns a baseline of log volume per workload and fires when volume spikes beyond the Z-Score threshold.

**Use cases:**

- Error log spike (application throwing exceptions)
- Log volume explosion (debug logging left on, retry storms)
- Sudden silence (service crashed, no logs at all)

## Pattern Matching

Fires immediately when specific patterns appear in logs, regardless of volume:

| Pattern | Meaning |
|---------|---------|
| `panic` | Go panic / unrecovered exception |
| `oom` / `out of memory` | Memory exhaustion |
| `fatal` | Fatal error, process likely dying |

!!! note "Severity"
    Pattern matches default to `critical` severity since they indicate process-level failures.

## Multi-Signal Correlation

Log anomalies participate in the correlation engine. When a log anomaly and a metric anomaly fire for the same workload within the correlation window (2 minutes), severity escalates:

```
warning (metric) + warning (log) → critical (correlated)
```

This catches scenarios like:

- CPU spike + error log spike → likely the same root cause
- Memory growth + OOM log → confirmed memory leak
