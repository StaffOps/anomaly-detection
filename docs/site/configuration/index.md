# Configuration

## Overview

All configuration lives in `controller/config.yaml`. The config loader expands `${VAR}` and `${VAR:default}` placeholders from environment variables before YAML parsing (12-Factor principle III).

## Environment Variables

Required variables (docker-compose fails fast if missing):

| Variable | Purpose | Example |
|----------|---------|---------|
| `VM_URL` | VictoriaMetrics endpoint | `https://victoria-metrics-read.example.com/select/0/prometheus` |
| `LOKI_URL` | Loki endpoint | `https://loki.example.com` |
| `ALERTMANAGER_URL` | Alertmanager endpoint | `https://alertmanager.example.com` |

Optional variables with defaults:

| Variable | Default | Purpose |
|----------|---------|---------|
| `CLUSTER_NAME` | `unknown` | Cluster identity label |
| `REDIS_ADDR` | `redis:6379` | Redis connection |
| `ML_ENDPOINT` | `ml:50051` | ML service gRPC endpoint |
| `ML_ENABLED` | `true` | Enable/disable ML |
| `WORKER_ENDPOINT` | `dns:///worker:50052` | Worker gRPC endpoint |
| `EXCLUDE_NAMESPACES_CSV` | `kube-system` | Fully excluded namespaces |
| `EXCLUDE_STATIC_ONLY_CSV` | (empty) | Static-only suppressed namespaces |
| `GRAFANA_BASE_URL` | (empty) | Base URL for Grafana deep links |
| `TEMPO_BASE_URL` | (empty) | Base URL for Tempo deep links |
| `LOKI_BASE_URL` | (empty) | Base URL for Loki deep links |
| `RUNBOOK_BASE_URL` | (empty) | Base URL for runbook links |

See `.env.example` at the repo root for the full list.

## Config Structure

```yaml
cluster: ${CLUSTER_NAME:unknown}

redis:
  addr: ${REDIS_ADDR:redis:6379}
  db: 0

datasources:
  victoriametrics:
    url: ${VM_URL}
    timeout: 30s
  loki:
    url: ${LOKI_URL}
    timeout: 30s
  alertmanager:
    url: ${ALERTMANAGER_URL}

ml:
  endpoint: ${ML_ENDPOINT:ml:50051}
  enabled: true
  timeout: 5s

controller:
  job_interval: 30s
  correlation_window: 2m
  cooldown: 5m
  metrics_port: 8080

baseline:
  ewma_alpha: 0.3
  zscore_threshold: 3.0
  warm_up_samples: 60

detection:
  static_rules: [...]       # See Detection Rules
  adaptive_metrics: [...]   # See Detection Rules
  log_patterns: [...]       # See Detection Rules

suppression: {...}          # See Suppression
enrichment: {...}           # See Enrichment
links: {...}                # Deep link URLs
```

## Hot Reload

The config file is watched for changes. When modified:

1. New rules are loaded without restart
2. Existing baselines are preserved
3. New detection rules start with warm-up phase
4. Removed rules stop immediately

!!! tip "Safe config changes"
    Use [Replay Mode](../operations/replay.md) to validate config changes against historical data before applying to the running system.
