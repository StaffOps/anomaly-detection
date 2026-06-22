# Tasks: Multivariate Per-Namespace Anomaly Detection (P2.3)

> **Status**: `FUTURE` — No external blockers, can implement after Phase 0

- [ ] Task 1: Implement namespace aggregator — collect per-cycle anomalies, group by `k8s.namespace.name`, emit aggregated struct per namespace
- [ ] Task 2: Add threshold configuration — `min_services` default 3, per-namespace overrides in config.yaml, hot-reload support
- [ ] Task 3: ML multivariate call — send feature vector `{anomalous_pod_count, distinct_services, max_severity, distinct_detectors}` to Isolation Forest via gRPC when threshold met (depends on: Task 1, Task 2)
- [ ] Task 4: Suppression logic — inhibit individual alerts for namespace/window when namespace pattern fires; resume on resolution (depends on: Task 3)
- [ ] Task 5: Alert annotations — include affected service names, pod count, severity, detectors, link placeholder for dependency graph (depends on: Task 3)
- [ ] Task 6: Expose metrics — `namespace_anomaly_detected_total{namespace}`, `namespace_anomaly_services_affected{namespace}` to Prometheus-compatible TSDB (depends on: Task 1)
- [ ] Task 7: Tests — unit tests for aggregator + threshold + suppression; integration test for full pipeline cycle with mock ML (depends on: Task 1–5)
- [ ] Task 8: Dashboard panel — Grafana panel showing namespace-level anomaly timeline, affected services heatmap (depends on: Task 6)
