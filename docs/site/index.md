# StaffOps Anomaly Detection

Distributed anomaly detection system for Kubernetes clusters. Combines adaptive statistical detection (Go) with ML-based forecasting and multivariate analysis (Python).

---

## What is this?

A complementary detection layer that sits alongside traditional alerting (VMAlert/Prometheus). It detects anomalies that static thresholds miss — gradual degradation, correlated failures, and workload-level patterns.

```mermaid
graph LR
    Prometheus[Prometheus] --> W[Workers]
    Loki --> W
    W --> C[Controller]
    C --> ML[ML Service]
    C --> AM[Alertmanager]
    AM --> Slack
```

## Key Features

| Feature | Description |
|---------|-------------|
| **Adaptive baselines** | EWMA + Welford's algorithm learns normal behavior per metric |
| **Multi-signal** | Metrics (Prometheus) + Logs (Loki) + K8s Events |
| **ML correlation** | Isolation Forest detects multivariate anomalies |
| **Workload-aware** | Groups pod-level anomalies into workload-level alerts |
| **Enrichment** | Alerts carry context (CPU ratio, memory, restarts, error rate) |
| **Deep links** | Grafana, Tempo, Loki links anchored at anomaly timestamp |
| **Replay mode** | Validate config changes against historical data offline |
| **Zero side effects** | Dry-run mode for safe rollout |

## Architecture at a Glance

```
Controller (Go)          Workers (Go, x3)         ML Service (Python)
┌──────────────┐        ┌──────────────┐         ┌──────────────┐
│ Scheduler    │──gRPC──│ Prometheus queries   │         │ Prophet      │
│ Correlator   │        │ Loki queries │         │ Isolation    │
│ Enrichment   │        │ Detection    │         │ Forest       │
│ Dispatcher   │──gRPC──│ Baselines    │         └──────┬───────┘
└──────┬───────┘        └──────┬───────┘                │
       │                       │                   gRPC │
       │                ┌──────▼───────┐                │
       │                │    Redis     │         ┌──────▼───────┐
       └────────────────│  Baselines   │─────────│  Controller  │
                        │  Dedup TTL   │         └──────────────┘
                        └──────────────┘
```

## Quick Navigation

- :material-sitemap: [**Architecture**](architecture/index.md) — System design, components, data flow
- :material-chart-bell-curve: [**Detection**](detection/index.md) — Algorithms, methods, correlation
- :material-cog: [**Configuration**](configuration/index.md) — Rules, suppression, enrichment
- :material-play-circle: [**Operations**](operations/index.md) — Quick start, replay, monitoring
- :material-code-braces: [**Development**](development/index.md) — Build, test, contribute

## Current Status

!!! success "Controller v0.11.0"
    - Static + Adaptive + Log detection, ML Isolation Forest (multivariate)
    - Workload-aware correlation + alert enrichment with deep links
    - **FDR (Benjamini-Hochberg)** over the full test family — controls
      multiple-comparison false positives
    - **Direction-of-badness** — adaptive rules fire only the bad way
    - Tuned rule set incl. **unbiased RED** (OTel SDK http metrics), DB latency,
      CPU throttling, and service-graph pipeline self-health
    - Replay mode with synthetic fault injection

!!! warning "Dry-run mode"
    Currently running in dry-run — alerts are generated but not dispatched to Alertmanager. Pending observability hardening (Phase 4) before production activation.
