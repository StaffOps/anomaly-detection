# Alert Rules Reference

When `vmRule.enabled=true`, the chart creates a `PrometheusRule` resource containing health alerts for the anomaly detection service itself. These rules monitor the controller, workers, and ML service — they are distinct from the detection rules you configure to find anomalies in your own workloads.

!!! note "Prerequisite"
    The `PrometheusRule` resource requires the [Prometheus Operator](https://github.com/Prometheus/operator) to be installed. If you are using the Prometheus Operator instead, the chart does not yet render a `PrometheusRule` automatically — use the custom alert example below to add rules manually.

Enable with:

```yaml
vmRule:
  enabled: true
  additionalLabels:
    vmalert: regional   # must match your VMAlert `ruleSelectorLabels`
```

---

## Included alerts

The table below lists all alerts shipped in the default `PrometheusRule`. The `for` duration is the minimum time the condition must be continuously true before the alert fires.

| Alert name | Condition | Severity | For | Description |
|---|---|---|---|---|
| `StaffOpsADControllerDown` | No controller pods are `Ready` | `critical` | `1m` | The controller Deployment has zero ready replicas. Detection cycles have stopped completely. |
| `StaffOpsADWorkerDown` | No worker pods are `Ready` | `critical` | `2m` | All worker replicas are unavailable. The controller cannot dispatch detection jobs. |
| `StaffOpsADMLServiceDown` | ML service pod is not `Ready` AND `ml.enabled=true` | `warning` | `5m` | The ML service is unavailable. Isolation Forest and Prophet detection are suspended; Z-Score and static rules continue. |
| `StaffOpsADControllerNoLeader` | `sum(staffops_ad_controller_is_leader) == 0` | `critical` | `1m` | No controller replica holds the Kubernetes Lease. This means no detection cycles are running, even though pods may appear healthy. Usually caused by RBAC issues preventing Lease acquisition. |
| `StaffOpsADHighDetectionLatency` | p95 cycle duration > 25s for 5m | `warning` | `5m` | Detection cycles are taking longer than 25 seconds (approaching the default 30s tick interval). Likely caused by slow datasource responses or too many concurrent detection rules. |
| `StaffOpsADBaselineWarmUp` | Any rule still has `staffops_ad_worker_baseline_warmup_remaining > 0` for more than 45m | `info` | `45m` | One or more adaptive rules have not completed baseline warm-up. This fires after install or after a Redis restart to signal that adaptive detection is not yet fully operational. Resolves automatically. |
| `StaffOpsADMLRetrainingFailing` | No successful Isolation Forest retraining in 3× `ml.retrainInterval` | `warning` | `0m` | The ML service has not completed a successful model retraining. The previous model version remains active, but may become stale. Check ML service logs for Python errors or OOMKilled events. |
| `StaffOpsADRedisUnavailable` | Worker Redis operation error rate > 50% for 2m | `critical` | `2m` | Workers cannot write to or read from Redis. Baselines are not being updated and deduplication is not functioning. This may cause alert floods if anomalies are re-fired on every cycle. |

---

## Alert detail

### StaffOpsADControllerDown

The controller is the orchestration brain of the system. When it is fully down, no detection runs and no alerts are produced. This is a complete system outage from an anomaly detection perspective.

**Typical causes:**
- Image pull failure (check `imagePullSecrets` and registry availability)
- CrashLoopBackOff due to misconfigured required values (missing datasource URLs)
- Node pressure causing pod eviction

**Runbook:** Check pod events with `kubectl describe pod -n monitoring -l app.kubernetes.io/component=controller`, then check logs with `kubectl logs`.

---

### StaffOpsADControllerNoLeader

This alert fires even when controller pods are running and `Ready`. It catches the specific case where both pods are healthy but neither holds the Lease — for example, after an RBAC change removed the `leases` update permission from the ServiceAccount.

```bash
# Inspect current lease state
kubectl get lease -n monitoring staffops-anomaly-detection-leader -o yaml

# Check RBAC permissions
kubectl auth can-i update leases \
  --as=system:serviceaccount:monitoring:<release>-controller \
  -n monitoring
```

---

### StaffOpsADHighDetectionLatency

The condition:

```promql
histogram_quantile(0.95,
  rate(staffops_ad_controller_cycle_duration_seconds_bucket[5m])
) > 25
```

A cycle duration approaching the `tickInterval` (default 30s) means the system is operating at its capacity limit. Cycles may start overlapping.

**Resolution options:**
- Increase `controller.tickInterval` to `60s`
- Reduce the number of `detection.adaptiveMetrics` rules
- Scale up workers (`worker.replicaCount`) or increase `worker.concurrency`
- Check per-datasource p99 latency: `histogram_quantile(0.99, rate(staffops_ad_worker_query_duration_seconds_bucket[5m]))`

---

### StaffOpsADBaselineWarmUp

This is an `info`-severity alert intended to be visible in dashboards and Slack/PagerDuty with low urgency. It auto-resolves once all rules complete warm-up. Expected after:
- Initial chart installation
- Redis pod restart with no persistence
- Adding new `adaptiveMetrics` rules

---

## Disabling individual alerts

To suppress a specific alert from the rendered PrometheusRule, list its name in `vmRule.disabledAlerts`:

```yaml
vmRule:
  enabled: true
  disabledAlerts:
    - StaffOpsADBaselineWarmUp    # suppress the info-level warm-up notification
    - StaffOpsADMLServiceDown     # suppress if ml.enabled=false
```

!!! warning "disabledAlerts vs Alertmanager inhibition"
    `disabledAlerts` removes the rule from the PrometheusRule resource entirely — the alert is never evaluated. This is different from Alertmanager inhibition or silences, which evaluate the rule but suppress the notification. Use `disabledAlerts` when you permanently don't want a rule; use Alertmanager silences for temporary suppression.

---

## Custom PrometheusRule example

To add your own rules that reference `staffops_ad_*` metrics, create a separate `PrometheusRule` resource rather than patching the chart's managed resource (which would be overwritten on upgrade).

```yaml title="custom-vmrule.yaml"
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: staffops-anomaly-detection-custom
  namespace: monitoring
  labels:
    vmalert: regional
spec:
  groups:
    - name: staffops-ad-custom
      interval: 60s
      rules:
        # Alert when the anomaly detection system produces zero detections
        # for an extended period — may indicate a broken pipeline.
        - alert: StaffOpsADNoDetectionsFor2h
          expr: |
            increase(staffops_ad_controller_anomalies_detected_total[2h]) == 0
              and
            staffops_ad_controller_is_leader == 1
          for: 5m
          labels:
            severity: warning
            team: platform
          annotations:
            summary: "Anomaly detection has produced zero detections in 2 hours"
            description: >
              The controller is running and leading, but no anomalies have been
              detected in the last 2 hours. This may indicate that all detection
              rules are misconfigured, or that datasource connectivity is silently
              failing without returning errors.
            runbook: "https://docs.example.com/runbooks/ad-no-detections"

        # Recording rule: pre-compute anomaly rate for dashboards
        - record: staffops_ad:anomaly_rate5m
          expr: |
            sum by (cluster, algorithm, severity) (
              rate(staffops_ad_controller_anomalies_detected_total[5m])
            )

        # Alert on sustained high Z-Score in production
        - alert: StaffOpsADPersistentHighZScore
          expr: |
            max by (cluster, rule) (
              staffops_ad_worker_zscore_current{cluster=~".+-prd"}
            ) > 4
          for: 10m
          labels:
            severity: critical
            team: platform
          annotations:
            summary: "Persistent Z-Score above 4 for rule {{ $labels.rule }}"
            description: >
              The adaptive detection rule {{ $labels.rule }} has maintained a
              Z-Score above 4 standard deviations for over 10 minutes in cluster
              {{ $labels.cluster }}. This is a strong signal of a real anomaly
              rather than noise.
```

Apply it:

```bash
kubectl apply -f custom-vmrule.yaml
```

---

## Adding custom alerts via values

For simpler cases, the chart supports injecting additional alert rules through the `vmRule.extraRules` value without creating a separate resource:

```yaml
vmRule:
  enabled: true
  extraRules:
    - alert: StaffOpsADWorkerQueueDepthHigh
      expr: |
        sum(staffops_ad_worker_jobs_total{result="success"}) by (cluster)
          /
        sum(staffops_ad_worker_jobs_total) by (cluster)
          < 0.8
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "Worker job success rate below 80% in {{ $labels.cluster }}"
```

!!! note "extraRules placement"
    Rules added via `vmRule.extraRules` are placed in the same `PrometheusRule` resource as the built-in alerts, under a separate group named `staffops-ad-custom`. They are subject to the same `vmRule.additionalLabels` and will be deleted if `vmRule.enabled` is set to `false`.

---

## PrometheusRule full resource reference

When `vmRule.enabled=true`, the chart renders a resource equivalent to the following. This is useful as a reference when creating a standalone `PrometheusRule` without the Helm chart, or when migrating to a `PrometheusRule` manually.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: <release>-staffops-anomaly-detection
  namespace: monitoring
spec:
  groups:
    - name: staffops-ad-health
      interval: 60s
      rules:
        - alert: StaffOpsADControllerDown
          expr: |
            kube_deployment_status_replicas_ready{
              deployment=~".*staffops-anomaly-detection.*controller.*"
            } == 0
          for: 1m
          labels:
            severity: critical
            component: controller
          annotations:
            summary: "Anomaly detection controller is down in {{ $labels.cluster }}"
            description: >
              The staffops-anomaly-detection controller Deployment has zero ready replicas.
              Detection cycles have stopped. Check pod events and image pull status.

        - alert: StaffOpsADWorkerDown
          expr: |
            kube_deployment_status_replicas_ready{
              deployment=~".*staffops-anomaly-detection.*worker.*"
            } == 0
          for: 2m
          labels:
            severity: critical
            component: worker
          annotations:
            summary: "All anomaly detection workers are down in {{ $labels.cluster }}"

        - alert: StaffOpsADMLServiceDown
          expr: |
            kube_deployment_status_replicas_ready{
              deployment=~".*staffops-anomaly-detection.*ml.*"
            } == 0
          for: 5m
          labels:
            severity: warning
            component: ml
          annotations:
            summary: "ML service is down — Isolation Forest and Prophet detection suspended"

        - alert: StaffOpsADControllerNoLeader
          expr: sum(staffops_ad_controller_is_leader) == 0
          for: 1m
          labels:
            severity: critical
            component: controller
          annotations:
            summary: "No anomaly detection controller leader in {{ $labels.cluster }}"
            description: >
              No controller replica holds the Kubernetes Lease. Detection is stopped.
              Check RBAC permissions for the controller ServiceAccount on the leases resource.

        - alert: StaffOpsADHighDetectionLatency
          expr: |
            histogram_quantile(0.95,
              rate(staffops_ad_controller_cycle_duration_seconds_bucket[5m])
            ) > 25
          for: 5m
          labels:
            severity: warning
            component: controller
          annotations:
            summary: "Detection cycle p95 latency is above 25s in {{ $labels.cluster }}"
            description: >
              Cycles are approaching the tick interval limit. Scale workers, reduce rule count,
              or increase controller.tickInterval.

        - alert: StaffOpsADBaselineWarmUp
          expr: count(staffops_ad_worker_baseline_warmup_remaining > 0) > 0
          for: 45m
          labels:
            severity: info
            component: worker
          annotations:
            summary: "Adaptive detection baselines still warming up in {{ $labels.cluster }}"
            description: >
              One or more adaptive detection rules have not completed baseline warm-up after
              45 minutes. Adaptive (Z-Score/EWMA) detection is not fully operational.
              Expected after install or after a Redis restart without persistence.

        - alert: StaffOpsADMLRetrainingFailing
          expr: |
            increase(staffops_ad_ml_retraining_total{result="success"}[3h]) == 0
              and
            on() hour() > 0   # avoid false positive in first 3h after deploy
          for: 0m
          labels:
            severity: warning
            component: ml
          annotations:
            summary: "ML model retraining has not succeeded in 3 hours"

        - alert: StaffOpsADRedisUnavailable
          expr: |
            rate(staffops_ad_worker_redis_operations_total{result="error"}[2m])
              /
            rate(staffops_ad_worker_redis_operations_total[2m])
              > 0.5
          for: 2m
          labels:
            severity: critical
            component: redis
          annotations:
            summary: "Redis error rate above 50% — baselines and dedup failing"
            description: >
              More than half of Redis operations are returning errors. Deduplication
              is not functioning, which may cause alert floods. Check Redis pod status
              and network connectivity from worker pods.
```
