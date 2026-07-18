# Installation

## Prerequisites

- Kubernetes ≥ 1.24
- Helm ≥ 3.10
- Prometheus with PromQL-compatible endpoint
- Loki with LogQL endpoint
- Alertmanager v2 endpoint

!!! note "What this chart does NOT install"
    Prometheus, Loki, and Alertmanager must already be running in your cluster. The anomaly detection service connects to them as data sources.

---

## Add the Helm repository

```bash
helm repo add staffops https://staffops.github.io/helm-charts/
helm repo update
```

---

## Quick install

```bash
helm install ad staffops/staffops-anomaly-detection \
  --namespace monitoring \
  --create-namespace \
  --set clusterName=my-cluster \
  --set datasources.victoriametrics.url=https://vm.example.com/select/0/prometheus \
  --set datasources.loki.url=https://loki.example.com \
  --set datasources.alertmanager.url=https://alertmanager.example.com
```

!!! warning "dryRun is enabled by default"
    The controller starts in `dryRun=true` mode — anomalies are logged but alerts are NOT sent to Alertmanager. Disable with `--set controller.dryRun=false` when ready for live alerting.

---

## Production install

Create a values file:

```yaml title="values-prd.yaml"
clusterName: prd-eks

datasources:
  prometheus:
    url: https://vm.internal/select/0/prometheus
  loki:
    url: https://loki.internal
  alertmanager:
    url: https://alertmanager.internal

controller:
  replicaCount: 2
  dryRun: false

redis:
  enabled: false
  external:
    addr: redis-prd.cache.amazonaws.com:6379
    existingSecret: redis-credentials

vmServiceScrape:
  enabled: true

vmRule:
  enabled: true

grafanaDashboard:
  enabled: true

links:
  grafanaBaseUrl: https://grafana.example.com
  runbookBaseUrl: https://docs.example.com/runbooks
```

```bash
helm install ad staffops/staffops-anomaly-detection \
  --namespace monitoring \
  --create-namespace \
  -f values-prd.yaml
```

---

## Verify the install

```bash
# Check pods
kubectl get pods -n monitoring -l app.kubernetes.io/name=staffops-anomaly-detection

# Controller readiness
kubectl exec -n monitoring deploy/ad-staffops-anomaly-detection-controller -- \
  wget -qO- http://localhost:8080/readyz

# Metrics endpoint
kubectl port-forward -n monitoring svc/ad-staffops-anomaly-detection-controller 8080:8080 &
curl -s localhost:8080/metrics | grep staffops_ad_controller_cycles_total
```

---

## Upgrade

```bash
helm repo update
helm upgrade ad staffops/staffops-anomaly-detection -n monitoring -f values-prd.yaml
```

!!! tip "Baseline preservation on upgrade"
    If `redis.persistence.enabled=true` or an external Redis is used, baselines survive upgrades. With in-cluster ephemeral Redis, baselines warm up again from scratch (~30 minutes).

---

## Uninstall

```bash
helm uninstall ad -n monitoring
```

If Redis persistence was enabled, delete the PVC manually:

```bash
kubectl delete pvc -n monitoring -l app.kubernetes.io/name=staffops-anomaly-detection
```
