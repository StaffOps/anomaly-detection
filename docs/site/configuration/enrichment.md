# Enrichment

## Overview

When an anomaly is detected, the enrichment engine queries additional context to help operators understand the situation without opening multiple dashboards.

## How It Works

1. Anomaly fires for a workload (e.g., `namespace=prod, pod=api-server-xyz`)
2. Enrichment engine identifies the **kind** (pod or service)
3. Executes the appropriate **bundle** of queries with template substitution
4. Results are added as alert annotations
5. Deep links are generated anchored at the anomaly timestamp

## Configuration

```yaml
enrichment:
  enabled: true
  cache_ttl: 30s           # Cache enrichment results
  query_timeout: 5s        # Per-query timeout
  max_concurrent: 5        # Bounded concurrency

  pod_bundle:
    - name: cpu_ratio
      query: 'max(rate(container_cpu_usage_seconds_total{namespace="$namespace",pod="$pod",container!=""}[1m])) / max(kube_pod_container_resource_limits{resource="cpu",namespace="$namespace",pod="$pod"})'
    - name: memory_ratio
      query: 'max(container_memory_working_set_bytes{namespace="$namespace",pod="$pod",container!=""}) / max(kube_pod_container_resource_limits{resource="memory",namespace="$namespace",pod="$pod"})'
    - name: restarts_5m
      query: 'max(increase(kube_pod_container_status_restarts_total{namespace="$namespace",pod="$pod"}[5m]))'
    - name: error_logs_1m
      source: loki
      query: 'sum(rate({k8s_namespace_name="$namespace",k8s_pod_name="$pod"} |~ "(?i)(error|panic|fatal)" [1m]))'

  service_bundle:
    - name: error_rate_1m
      query: 'sum(rate(spanmetrics_apm_calls_total{service_name="$service_name",status_code="STATUS_CODE_ERROR"}[1m]))'
    - name: request_rate_1m
      query: 'sum(rate(spanmetrics_apm_calls_total{service_name="$service_name"}[1m]))'
    - name: latency_p99_5m
      query: 'histogram_quantile(0.99, sum(rate(spanmetrics_apm_duration_milliseconds_bucket{service_name="$service_name"}[5m])) by (le))'
```

## Template Variables

| Variable | Source | Available in |
|----------|--------|-------------|
| `$namespace` | Anomaly labels | Pod bundle |
| `$pod` | Anomaly labels | Pod bundle |
| `$service_name` | Anomaly labels | Service bundle |

## Bundles

### Pod Bundle

Queries executed for pod-level anomalies:

| Name | What it measures |
|------|-----------------|
| `cpu_ratio` | CPU usage / CPU limit (0-1) |
| `memory_ratio` | Memory usage / Memory limit (0-1) |
| `restarts_5m` | Container restarts in last 5 minutes |
| `oom_kills` | OOMKilled termination reason |
| `ready_replicas` | Pod readiness status |
| `error_logs_1m` | Error/panic/fatal log rate (Loki) |

### Service Bundle

Queries executed for service-level anomalies:

| Name | What it measures |
|------|-----------------|
| `error_rate_1m` | Error rate from span metrics |
| `request_rate_1m` | Total request rate |
| `latency_p99_5m` | P99 latency |

## Deep Links

Alert annotations include clickable links to observability tools, anchored at the anomaly timestamp (±15min for metrics/traces, ±5min for logs):

```yaml
links:
  grafana_base_url: ${GRAFANA_BASE_URL:}
  tempo_base_url: ${TEMPO_BASE_URL:}
  loki_base_url: ${LOKI_BASE_URL:}
  runbook_base_url: ${RUNBOOK_BASE_URL:}
  grafana_vm_datasource_uid: ${GRAFANA_VM_DATASOURCE_UID:}
  grafana_tempo_datasource_uid: ${GRAFANA_TEMPO_DATASOURCE_UID:}
  grafana_loki_datasource_uid: ${GRAFANA_LOKI_DATASOURCE_UID:}
```

Generated annotations:

| Annotation | Target | Content |
|------------|--------|---------|
| `grafana_url` | Grafana Explore | PromQL query for the anomalous metric |
| `tempo_url` | Grafana Explore (Tempo) | TraceQL filtered by service |
| `loki_url` | Grafana Explore (Loki) | LogQL filtered by namespace/pod |
| `runbook_url` | Runbook docs | Per-detector runbook page |

!!! note "Empty URLs"
    If a `*_base_url` env var is empty, the corresponding annotation is not emitted. This allows running without Grafana configured.

## Caching

Enrichment results are cached in Redis with configurable TTL (default 30s). Cache key is `namespace + pod` (or `service_name` for service bundle).

!!! info "Cache effectiveness"
    In practice, cache hit rate is low because the dedup cooldown (5min) prevents the same workload from being enriched again within the cache TTL. This is expected behavior — the cache primarily helps when multiple anomalies fire for the same workload in the same cycle.
