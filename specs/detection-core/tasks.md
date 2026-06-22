# Tasks: Detection Core (Phase 1)

> **Status**: `DONE` — Implemented in controller 0.7.0 (2026-05-30)

## P1.1 — Label-based Pivot (Anomaly Enrichment)

- [x] Task 1: Implement `IdentityExtractor` with regex patterns for pod and service workload kinds
- [x] Task 2: Define default metric bundles (cpu_ratio, memory_ratio, restarts_5m, error_rate_1m, latency_p99_5m)
- [x] Task 3: Implement template substitution for PromQL queries with extracted identity
- [x] Task 4: Add Redis LRU cache (SET with TTL, GET with fallback to live query)
- [x] Task 5: Implement bounded concurrency via semaphore for enrichment queries
- [x] Task 6: Integrate enrichment results into alert payload annotations
- [x] Task 7: Unit tests for identity extraction, cache hit/miss, template rendering

## P1.2 — Alert Payload with Links

- [x] Task 8: Implement `LinkBuilder` with Grafana Explore URL renderer (MetricsQL, ±15min)
- [x] Task 9: Add Tempo TraceQL link renderer (±15min)
- [x] Task 10: Add Loki LogQL link renderer (±5min)
- [x] Task 11: Add per-detector Runbook URL renderer
- [x] Task 12: Implement label specificity fallback (pod → workload → namespace)
- [x] Task 13: Write 6 unit tests (one per link type + edge cases)

## P1.3 — Complete Readiness Checks

- [x] Task 14: Implement `/readyz` endpoint with parallel probe execution
- [x] Task 15: Add Redis probe (PING, 3s timeout)
- [x] Task 16: Add VictoriaMetrics probe (query `up`, 3s timeout)
- [x] Task 17: Add Loki probe (ready endpoint, 3s timeout)
- [x] Task 18: Add Alertmanager probe (status endpoint, 3s timeout)
- [x] Task 19: Add ML service probe (gRPC Health check, no-op when disabled)
- [x] Task 20: Add metric `staffops_ad_controller_readiness_checks_total{dependency,result}`
- [x] Task 21: Write 7 unit tests (success, timeout, ML-disabled, partial failure, all-fail, metric increment, JSON response)
