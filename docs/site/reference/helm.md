# Helm Chart Reference

Chart: `staffops/staffops-anomaly-detection`
Repository: `https://staffops.github.io/helm-charts/`

This page is a complete reference for every value exposed by the chart. For a guided walkthrough of the most common configurations, see [Configuration](../getting-started/configuration.md).

---

## Required values

These three values have no defaults and **must** be provided. The chart will fail to render without them.

| Key | Type | Description |
|---|---|---|
| `clusterName` | string | Cluster identifier. Injected as the `cluster` label on every Prometheus metric and Alertmanager alert. |
| `datasources.victoriametrics.url` | string | Prometheus PromQL-compatible read endpoint, e.g. `http://vmselect:8481/select/0/prometheus`. |
| `datasources.loki.url` | string | Loki HTTP read endpoint, e.g. `http://loki-gateway:3100`. |
| `datasources.alertmanager.url` | string | Alertmanager v2 API base URL, e.g. `http://alertmanager:9093`. |

!!! warning "TLS endpoints"
    If your datasources use HTTPS with self-signed certificates, set `datasources.*.tlsInsecureSkipVerify: true` for each affected source, or mount a CA bundle via `extraVolumes` and `extraVolumeMounts`.

Minimal values file:

```yaml title="values-minimal.yaml"
clusterName: my-cluster

datasources:
  prometheus:
    url: http://vmselect.monitoring.svc:8481/select/0/prometheus
  loki:
    url: http://loki-gateway.monitoring.svc:3100
  alertmanager:
    url: http://alertmanager.monitoring.svc:9093
```

---

## Controller

The controller orchestrates detection cycles and is deployed as a Deployment with Kubernetes Lease-based leader election. Only the Lease holder executes cycles; the standby replica remains healthy but idle.

| Key | Default | Type | Description |
|---|---|---|---|
| `controller.replicaCount` | `2` | integer | Number of controller replicas. Two replicas are required for HA. A single replica disables leader election automatically. |
| `controller.dryRun` | `true` | bool | When `true`, anomalies are logged and the `staffops_ad_controller_alerts_fired_total` counter is incremented, but **no alerts are sent to Alertmanager**. Disable when ready for live alerting. |
| `controller.tickInterval` | `30s` | duration | Interval between detection cycles. Valid suffixes: `s`, `m`. Shorter intervals increase load on datasources. |
| `controller.leaderElection.enabled` | `true` | bool | Enable Kubernetes Lease leader election. Disable only for single-replica setups. |
| `controller.leaderElection.leaseDuration` | `15s` | duration | How long a lease is held before it must be renewed. |
| `controller.leaderElection.renewDeadline` | `10s` | duration | Time the leader has to renew the lease before losing it. |
| `controller.leaderElection.retryPeriod` | `2s` | duration | How often a standby replica attempts to acquire the lease. |
| `controller.resources.requests.cpu` | `100m` | string | CPU request for the controller container. |
| `controller.resources.requests.memory` | `128Mi` | string | Memory request for the controller container. |
| `controller.resources.limits.memory` | `256Mi` | string | Memory limit for the controller container. |
| `controller.env` | `[]` | list | Extra environment variables injected into the controller pod. |
| `controller.podAnnotations` | `{}` | map | Annotations applied to controller pods, e.g. for Vault Agent injection. |
| `controller.tolerations` | `[]` | list | Tolerations for controller pod scheduling. |
| `controller.affinity` | `{}` | map | Affinity rules for controller pod scheduling. |

```yaml title="Controller HA example"
controller:
  replicaCount: 2
  dryRun: false
  tickInterval: 30s
  leaderElection:
    enabled: true
    leaseDuration: 15s
    renewDeadline: 10s
    retryPeriod: 2s
  resources:
    requests:
      cpu: 200m
      memory: 256Mi
    limits:
      memory: 512Mi
```

---

## Workers

Workers are stateless Go processes that execute PromQL and LogQL queries, run detection algorithms, and read/write baselines in Redis. They receive jobs from the controller via gRPC.

| Key | Default | Type | Description |
|---|---|---|---|
| `worker.replicaCount` | `3` | integer | Number of worker replicas. Workers are stateless — scale freely. |
| `worker.concurrency` | `5` | integer | Number of detection jobs a single worker processes in parallel. Higher values increase throughput but also datasource load. |
| `worker.queryTimeout` | `20s` | duration | Per-query timeout for Prometheus and Loki requests. |
| `worker.grpc.port` | `9090` | integer | gRPC port the worker listens on. |
| `worker.resources.requests.cpu` | `200m` | string | CPU request per worker pod. |
| `worker.resources.requests.memory` | `256Mi` | string | Memory request per worker pod. |
| `worker.resources.limits.memory` | `512Mi` | string | Memory limit per worker pod. |
| `worker.autoscaling.enabled` | `false` | bool | Enable HPA for workers. |
| `worker.autoscaling.minReplicas` | `2` | integer | HPA minimum replicas. |
| `worker.autoscaling.maxReplicas` | `10` | integer | HPA maximum replicas. |
| `worker.autoscaling.targetCPUUtilizationPercentage` | `70` | integer | HPA target CPU utilization. |
| `worker.env` | `[]` | list | Extra environment variables for worker pods. |
| `worker.tolerations` | `[]` | list | Tolerations for worker pod scheduling. |

```yaml title="Worker scaling example"
worker:
  replicaCount: 5
  concurrency: 8
  queryTimeout: 30s
  autoscaling:
    enabled: true
    minReplicas: 3
    maxReplicas: 12
    targetCPUUtilizationPercentage: 65
```

---

## ML Service

The ML service is a Python FastAPI application that exposes Isolation Forest and Prophet inference endpoints consumed by workers via gRPC.

| Key | Default | Type | Description |
|---|---|---|---|
| `ml.enabled` | `true` | bool | Deploy the ML service. When `false`, workers skip ML-based detection and only run Z-Score/EWMA/static rules. |
| `ml.image.repository` | `ghcr.io/staffops/anomaly-detection-ml` | string | ML service container image repository. |
| `ml.image.tag` | `""` | string | Image tag. Defaults to the chart's `appVersion`. |
| `ml.image.pullPolicy` | `IfNotPresent` | string | Image pull policy. |
| `ml.retrainInterval` | `60m` | duration | How often Isolation Forest models are retrained on recent Redis baselines. |
| `ml.isolationForest.contamination` | `0.05` | float | Expected fraction of anomalies in training data (scikit-learn `contamination` parameter). |
| `ml.isolationForest.nEstimators` | `100` | integer | Number of trees in the Isolation Forest ensemble. |
| `ml.prophet.enabled` | `true` | bool | Enable Prophet forecasting within the ML service. |
| `ml.prophet.forecastHorizonMinutes` | `30` | integer | How far ahead Prophet forecasts expected values. |
| `ml.prophet.uncertaintyInterval` | `0.95` | float | Confidence interval for anomaly detection (0–1). Values outside this interval are flagged. |
| `ml.resources.requests.cpu` | `500m` | string | CPU request for the ML service pod. |
| `ml.resources.requests.memory` | `512Mi` | string | Memory request for the ML service pod. |
| `ml.resources.limits.memory` | `1Gi` | string | Memory limit for the ML service pod. |
| `ml.grpc.port` | `50051` | integer | gRPC port the ML service listens on. |

!!! note "Prophet data requirements"
    Prophet requires at least **2 weeks of historical data** stored in Redis baselines before it can generate reliable forecasts. During the initial warm-up period, Prophet-based detection is skipped and only Z-Score and Isolation Forest run.

```yaml title="ML service tuning example"
ml:
  enabled: true
  retrainInterval: 30m
  isolationForest:
    contamination: 0.03
    nEstimators: 150
  prophet:
    enabled: true
    forecastHorizonMinutes: 60
    uncertaintyInterval: 0.99
  resources:
    requests:
      cpu: 1000m
      memory: 1Gi
    limits:
      memory: 2Gi
```

---

## Redis

Redis stores metric baselines (mean, stddev, EWMA), seasonal profiles, and deduplication TTLs. The chart can deploy an in-cluster Redis instance or connect to an external one.

| Key | Default | Type | Description |
|---|---|---|---|
| `redis.enabled` | `true` | bool | Deploy an in-cluster Redis instance. Set to `false` when using an external Redis. |
| `redis.image.repository` | `redis` | string | Redis image repository. |
| `redis.image.tag` | `7.2-alpine` | string | Redis image tag. |
| `redis.persistence.enabled` | `false` | bool | Enable PVC for Redis data. When `false`, baselines are lost on pod restart. |
| `redis.persistence.size` | `2Gi` | string | PVC size for Redis persistence. |
| `redis.persistence.storageClass` | `""` | string | StorageClass for the PVC. Defaults to the cluster default. |
| `redis.resources.requests.cpu` | `100m` | string | CPU request for the Redis pod. |
| `redis.resources.requests.memory` | `128Mi` | string | Memory request for the Redis pod. |
| `redis.resources.limits.memory` | `256Mi` | string | Memory limit for the Redis pod. |
| `redis.external.addr` | `""` | string | Address of external Redis (`host:port`). Required when `redis.enabled=false`. |
| `redis.external.existingSecret` | `""` | string | Name of an existing Kubernetes Secret containing the key `redis-password`. |
| `redis.ttl.baseline` | `72h` | duration | TTL for metric baseline entries in Redis. |
| `redis.ttl.dedup` | `1h` | duration | TTL for alert deduplication keys. Duplicate anomalies are suppressed for this window. |
| `redis.ttl.seasonalProfile` | `336h` | duration | TTL for seasonal profile data (default: 14 days). |

=== "In-cluster Redis with persistence"

    ```yaml
    redis:
      enabled: true
      persistence:
        enabled: true
        size: 5Gi
        storageClass: gp3
      resources:
        requests:
          cpu: 200m
          memory: 256Mi
        limits:
          memory: 512Mi
    ```

=== "External Redis (AWS ElastiCache)"

    ```yaml
    redis:
      enabled: false
      external:
        addr: my-redis.abc123.ng.0001.use1.cache.amazonaws.com:6379
        existingSecret: redis-credentials
    ```

    The referenced Secret must contain:

    ```yaml
    apiVersion: v1
    kind: Secret
    metadata:
      name: redis-credentials
      namespace: monitoring
    type: Opaque
    stringData:
      redis-password: "s3cr3t"
    ```

!!! warning "Baseline warm-up after restart"
    If `redis.persistence.enabled=false` (the default) and the Redis pod restarts, all baselines are lost. Workers will re-enter the warm-up period (~30 minutes with default settings) before adaptive detection resumes.

---

## Detection rules

Detection rules define what the workers look for during each tick cycle. Three types are supported: static threshold expressions, adaptive Z-Score metrics, and Loki log pattern rules.

### Static rules

Static rules evaluate a PromQL or LogQL expression directly. They fire when the expression returns results.

```yaml
detection:
  staticRules:
    - name: high-cpu-saturation
      expr: 'avg by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!=""}[5m])) > 0.9'
      severity: warning
      for: 5m
      labels:
        team: platform
        component: compute
      annotations:
        summary: "Pod {{ $labels.pod }} CPU saturation above 90%"
        runbook: "https://docs.example.com/runbooks/high-cpu"

    - name: disk-near-full
      expr: '(node_filesystem_avail_bytes / node_filesystem_size_bytes) < 0.10'
      severity: critical
      for: 2m
      labels:
        team: infra
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique rule name. Used as the `alertname` label. |
| `expr` | yes | PromQL expression. Fires when the expression returns a non-empty result set. |
| `severity` | yes | Alert severity: `info`, `warning`, or `critical`. |
| `for` | no | Duration the expression must remain true before firing. Defaults to `0s`. |
| `labels` | no | Extra labels merged into the alert. |
| `annotations` | no | Annotations added to the Alertmanager payload. Supports Go template variables. |

### Adaptive metrics (Z-Score / EWMA)

Adaptive metrics compute a rolling baseline and flag deviations beyond a configurable number of standard deviations. EWMA smoothing is applied to prevent single noisy samples from triggering false positives.

```yaml
detection:
  adaptiveMetrics:
    - name: http-request-rate-anomaly
      query: 'sum by (service) (rate(http_requests_total{namespace="production"}[5m]))'
      window: 2h          # baseline rolling window
      threshold: 3.0      # Z-Score standard deviations
      severity: warning
      labels:
        team: backend

    - name: p99-latency-spike
      query: 'histogram_quantile(0.99, sum by (service, le) (rate(http_request_duration_seconds_bucket[5m])))'
      window: 1h
      threshold: 2.5
      severity: critical
      labels:
        team: backend
        slo: true

    - name: queue-depth-unusual
      query: 'rabbitmq_queue_messages{vhost="production"}'
      window: 30m
      threshold: 4.0
      severity: warning
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique rule name. |
| `query` | yes | PromQL query. The returned time series are evaluated individually against their own baseline. |
| `window` | yes | Rolling window for baseline computation. |
| `threshold` | yes | Z-Score threshold in standard deviations. |
| `severity` | no | Defaults to `warning`. |
| `labels` | no | Extra labels merged into alerts. |

### Log patterns (Loki)

Log pattern rules execute LogQL stream selectors against Loki and fire when the occurrence count within a window exceeds a threshold.

```yaml
detection:
  logPatterns:
    - name: oom-kill-detected
      query: '{namespace="production"} |= "OOMKilled"'
      threshold: 3         # occurrences per window
      window: 10m
      severity: critical
      labels:
        team: platform

    - name: database-connection-errors
      query: '{app="api-server"} |~ "connection refused|dial tcp.*:5432"'
      threshold: 10
      window: 5m
      severity: warning
      labels:
        team: backend

    - name: panic-in-production
      query: '{namespace=~"production|staging"} |= "panic:"'
      threshold: 1
      window: 5m
      severity: critical
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Unique rule name. |
| `query` | yes | LogQL stream selector. Supports `|=`, `!=`, `|~`, `!~` filter expressions. |
| `threshold` | yes | Minimum number of log line matches within `window` to trigger an anomaly. |
| `window` | yes | Time window for occurrence counting. |
| `severity` | no | Defaults to `warning`. |
| `labels` | no | Extra labels added to the alert. |

---

## Suppression

Suppression rules prevent detection from running in, or alerting on, specific namespaces or workloads.

| Key | Default | Type | Description |
|---|---|---|---|
| `suppression.excludeNamespaces` | `["kube-system"]` | list | Namespaces fully excluded from all detection. No queries are run, no alerts are fired. |
| `suppression.excludeStaticOnly` | `[]` | list | Namespaces where static threshold rules are suppressed, but adaptive and ML-based detection still run and alert. |
| `suppression.excludeAdaptiveWorkloads` | `[]` | list | Workloads whose adaptive (Z-Score) detections are suppressed while static/log signals still fire. For inherently bursty infra (brokers, collectors, service mesh). Namespace-independent. |

```yaml
suppression:
  # No detection at all in infrastructure namespaces
  excludeNamespaces:
    - kube-system
    - cert-manager
    - flux-system
    - monitoring

  # In staging: suppress noisy static thresholds but keep adaptive detection
  excludeStaticOnly:
    - staging
    - qa

  # Silence adaptive noise from bursty infra workloads (static/logs still fire)
  excludeAdaptiveWorkloads:
    - strimzi-kafka-brokers
    - otel-agent-logs-collector
    - istiod
```

!!! note "excludeStaticOnly use case"
    `excludeStaticOnly` is designed for staging or QA environments where hard threshold rules produce too many false positives (e.g., intentional load tests breaching CPU thresholds), but unusual behavioral patterns — detected by Z-Score or Isolation Forest — should still be flagged.

!!! tip "excludeAdaptiveWorkloads use case"
    Use this for individual infrastructure workloads (Kafka brokers, OTel collectors, Istio, Pyroscope) that are inherently bursty and dominate false positives, but share a namespace with real application workloads — so namespace-level suppression is too blunt. Only their adaptive signal is silenced; static breaches (OOM, restart loops) and log patterns still alert. The effect is observable via `staffops_ad_worker_anomalies_suppressed_total{reason="adaptive_workload"}`.

---

## Observability

### serviceMonitor

Prometheus Operator ServiceMonitor for scraping controller and worker metrics.

| Key | Default | Description |
|---|---|---|
| `serviceMonitor.enabled` | `false` | Create a ServiceMonitor resource. |
| `serviceMonitor.namespace` | `""` | Namespace for the ServiceMonitor. Defaults to the release namespace. |
| `serviceMonitor.interval` | `30s` | Scrape interval. |
| `serviceMonitor.scrapeTimeout` | `10s` | Per-scrape timeout. |
| `serviceMonitor.labels` | `{}` | Extra labels on the ServiceMonitor, used for Prometheus selector matching. |

### vmServiceScrape

Prometheus Operator ServiceMonitor (preferred when using the Prometheus stack).

| Key | Default | Description |
|---|---|---|
| `vmServiceScrape.enabled` | `false` | Create a ServiceMonitor resource. |
| `vmServiceScrape.interval` | `30s` | Scrape interval. |
| `vmServiceScrape.scrapeTimeout` | `10s` | Per-scrape timeout. |
| `vmServiceScrape.extraEndpoints` | `[]` | Additional scrape endpoints, e.g. for the ML service. |

### vmRule

Prometheus Operator PrometheusRule containing health alerts and recording rules for the anomaly detection service itself.

| Key | Default | Description |
|---|---|---|
| `vmRule.enabled` | `false` | Create the PrometheusRule resource. |
| `vmRule.namespace` | `""` | Namespace for the PrometheusRule. Defaults to the release namespace. |
| `vmRule.additionalLabels` | `{}` | Extra labels for VMAlertmanager rule group selection. |
| `vmRule.disabledAlerts` | `[]` | List of alert names to exclude from the rendered PrometheusRule. |

### grafanaDashboard

| Key | Default | Description |
|---|---|---|
| `grafanaDashboard.enabled` | `false` | Create a ConfigMap with the Grafana dashboard JSON. |
| `grafanaDashboard.label` | `grafana_dashboard` | Label key applied to the ConfigMap for Grafana sidecar discovery. |
| `grafanaDashboard.labelValue` | `"1"` | Label value for sidecar discovery. |
| `grafanaDashboard.namespace` | `""` | Namespace for the ConfigMap. Defaults to the release namespace. |
| `grafanaDashboard.folder` | `StaffOps` | Grafana folder where the dashboard is placed. |

```yaml title="Full observability stack values"
serviceMonitor:
  enabled: false   # use vmServiceScrape instead if running vm-operator

vmServiceScrape:
  enabled: true
  interval: 15s

vmRule:
  enabled: true
  additionalLabels:
    vmalert: regional  # match your VMAlert label selector

grafanaDashboard:
  enabled: true
  label: grafana_dashboard
  labelValue: "1"
  folder: StaffOps
```

### Links (for dashboard and alert annotations)

| Key | Default | Description |
|---|---|---|
| `links.grafanaBaseUrl` | `""` | Grafana base URL injected into alert annotations as deep-link targets. |
| `links.runbookBaseUrl` | `""` | Runbook base URL prepended to per-alert runbook paths in annotations. |

---

## Image overrides

By default all images are pulled from `ghcr.io/staffops`. These values allow overriding individual components, for example when using an internal registry mirror.

| Key | Default | Description |
|---|---|---|
| `image.registry` | `ghcr.io` | Global registry prefix. Applies to all components unless overridden per-component. |
| `controller.image.repository` | `staffops/anomaly-detection-controller` | Controller image repository (appended to registry). |
| `controller.image.tag` | `""` | Controller image tag. Defaults to chart `appVersion`. |
| `controller.image.pullPolicy` | `IfNotPresent` | Controller image pull policy. |
| `worker.image.repository` | `staffops/anomaly-detection-worker` | Worker image repository. |
| `worker.image.tag` | `""` | Worker image tag. Defaults to chart `appVersion`. |
| `worker.image.pullPolicy` | `IfNotPresent` | Worker image pull policy. |
| `ml.image.repository` | `staffops/anomaly-detection-ml` | ML service image repository. |
| `ml.image.tag` | `""` | ML service image tag. Defaults to chart `appVersion`. |
| `ml.image.pullPolicy` | `IfNotPresent` | ML service image pull policy. |
| `imagePullSecrets` | `[]` | List of image pull secret names applied to all pods. |

```yaml title="Internal registry mirror example"
image:
  registry: registry.corp.example.com

imagePullSecrets:
  - name: corp-registry-credentials

controller:
  image:
    tag: v1.4.2-patched

ml:
  image:
    pullPolicy: Always
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| No alerts fire despite known anomalies | `controller.dryRun=true` (the default) | Set `controller.dryRun=false` and run `helm upgrade`. Verify with `kubectl logs deploy/<release>-controller \| grep dryRun`. |
| Controller pods both show `Standby` in logs, no cycles run | Two controller pods cannot reach the Kubernetes API to acquire the Lease | Check RBAC: the chart's ServiceAccount needs `leases` get/create/update permissions in the release namespace. Run `kubectl auth can-i update leases --as=system:serviceaccount:monitoring:<release>-controller`. |
| Workers log `baseline not ready, skipping adaptive detection` | Redis baselines have not warmed up yet | Wait ~30 minutes after install or after a Redis restart. The metric `staffops_ad_worker_baseline_warmup_remaining` tracks remaining warm-up samples. |
| ML service pod restarts repeatedly with OOMKilled | Isolation Forest training on large datasets exceeds memory limit | Increase `ml.resources.limits.memory` (start with `2Gi`) or reduce `ml.isolationForest.nEstimators`. |
| `staffops_ad_worker_redis_cache_hits_total` is zero | Workers cannot connect to Redis | Verify Redis address, port, and password. For external Redis, confirm the Secret referenced by `redis.external.existingSecret` exists in the same namespace and contains the `redis-password` key. |
| Alerts arrive in Alertmanager but without the `cluster` label | `clusterName` was not set | Set `clusterName` in values and run `helm upgrade`. All metrics and alerts carry this label. |
| Prophet detection never triggers | Insufficient historical data in Redis | Prophet requires at least 2 weeks of baseline data. Check `staffops_ad_ml_prophet_training_samples` — it must exceed `336` (2 weeks × 24h × 7 samples/h at 30s tick). |
| High query latency warnings in worker logs | Slow Prometheus or Loki responses | Tune `worker.queryTimeout`, reduce `worker.concurrency`, or check datasource health. The histogram `staffops_ad_worker_query_duration_seconds` shows per-datasource p99 latency. |
