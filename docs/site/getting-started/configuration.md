# Configuration

All configuration is done via `values.yaml`. Below are the key parameters.

---

## Required

| Key | Description |
|---|---|
| `clusterName` | Cluster identifier — used as the `cluster` label on all metrics and alerts |
| `datasources.victoriametrics.url` | VictoriaMetrics read endpoint (PromQL-compatible) |
| `datasources.loki.url` | Loki read endpoint (LogQL) |
| `datasources.alertmanager.url` | Alertmanager v2 API endpoint |

---

## Controller

| Key | Default | Description |
|---|---|---|
| `controller.replicaCount` | `2` | Replicas (HA via Kubernetes Lease) |
| `controller.dryRun` | `true` | Log alerts without dispatching to Alertmanager |
| `controller.leaderElection.enabled` | `true` | K8s Lease leader election |
| `controller.tickInterval` | `30s` | Interval between detection cycles |

---

## Workers

| Key | Default | Description |
|---|---|---|
| `worker.replicaCount` | `3` | Worker replicas (stateless, horizontally scalable) |
| `worker.concurrency` | `5` | Parallel detection jobs per worker |

---

## ML Service

| Key | Default | Description |
|---|---|---|
| `ml.enabled` | `true` | Enable the Python ML service |
| `ml.image.repository` | `ghcr.io/staffops/anomaly-detection-ml` | ML service image |

---

## Redis

| Key | Default | Description |
|---|---|---|
| `redis.enabled` | `true` | Deploy in-cluster Redis |
| `redis.persistence.enabled` | `false` | PVC for Redis data (survives pod restarts) |
| `redis.external.addr` | `""` | External Redis address when `redis.enabled=false` |
| `redis.external.existingSecret` | `""` | Secret with `redis-password` key |

---

## Detection rules

### Static thresholds

```yaml
detection:
  staticRules:
    - name: high-cpu
      expr: 'avg by (pod) (rate(container_cpu_usage_seconds_total[5m])) > 0.9'
      severity: warning
      labels:
        team: platform
```

### Adaptive (Z-Score)

```yaml
detection:
  adaptiveMetrics:
    - name: request-rate
      query: 'sum(rate(http_requests_total[5m]))'
      window: 1h
      threshold: 3.0   # standard deviations
```

### Log patterns (Loki)

```yaml
detection:
  logPatterns:
    - name: oom-kill
      query: '{namespace="production"} |= "OOMKilled"'
      threshold: 5     # occurrences per window
      window: 10m
```

---

## Suppression

```yaml
suppression:
  excludeNamespaces:
    - kube-system
    - monitoring
  excludeStaticOnly:
    - staging    # adaptive still fires, static rules suppressed
```

---

## Observability integrations

| Key | Default | Description |
|---|---|---|
| `serviceMonitor.enabled` | `false` | Prometheus Operator ServiceMonitor |
| `vmServiceScrape.enabled` | `false` | vm-operator VMServiceScrape |
| `vmRule.enabled` | `false` | vm-operator VMRule (health alerts + recording rules) |
| `grafanaDashboard.enabled` | `false` | ConfigMap labelled for Grafana sidecar discovery |
