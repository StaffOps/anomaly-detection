# Feature: Detection Core (Phase 1)

Covers P1.1 (Enrichment), P1.2 (Alert Links), P1.3 (Readiness Checks) — all shipped in controller 0.7.0.

## User Stories

### US-1: On-call SRE receiving an alert

WHEN an anomaly is detected and an alert fires
THEN the alert payload SHALL contain enriched context (cpu_ratio, memory_ratio, restarts_5m, error_rate_1m, latency_p99_5m) and direct links to Grafana Explore, Tempo traces, and Loki logs anchored at the anomaly timestamp, so the SRE can begin investigation without manual query construction.

### US-2: Grafana user correlating signals

WHEN a user clicks an alert link in Slack
THEN the link SHALL open the correct Grafana Explore panel with pre-filled query (MetricsQL, TraceQL, or LogQL) scoped to the affected workload and time window (±15min for metrics/traces, ±5min for logs).

### US-3: Operator checking controller health

WHEN an operator or K8s probe hits `/readyz`
THEN the endpoint SHALL report aggregate health of all dependencies (Redis, Prometheus-compatible TSDB, Loki, Alertmanager, ML service) with each probe capped at 3s timeout, and the ML probe SHALL be no-op when the ML service is disabled.

## Acceptance Criteria

### P1.1 — Label-based Pivot (Anomaly Enrichment)

- [x] Identity extraction via regex patterns for `pod` and `service` workload kinds
- [x] Template substitution resolves PromQL queries with extracted identity labels
- [x] Bounded concurrency on enrichment queries (semaphore-limited)
- [x] Redis-backed LRU cache with TTL avoids redundant queries within a cycle
- [x] Default metric bundles per kind: `cpu_ratio`, `memory_ratio`, `restarts_5m`, `error_rate_1m`, `latency_p99_5m`
- [x] Enrichment results injected into alert payload annotations

### P1.2 — Alert Payload with Links

- [x] LinkBuilder renders Grafana Explore URL (MetricsQL) anchored at anomaly timestamp ±15min
- [x] LinkBuilder renders Tempo TraceQL URL anchored at ±15min
- [x] LinkBuilder renders Loki LogQL URL anchored at ±5min
- [x] LinkBuilder renders per-detector Runbook URL
- [x] Links use most-specific labels available (namespace, workload, pod)
- [x] 6 unit tests covering all link types and edge cases

### P1.3 — Complete Readiness Checks

- [x] `/readyz` endpoint probes: Redis, Prometheus-compatible TSDB, Loki, Alertmanager, ML service
- [x] Each probe capped at 3s independent timeout
- [x] ML probe returns healthy (no-op) when ML service is disabled in config
- [x] Metric `staffops_ad_controller_readiness_checks_total{dependency,result}` incremented per check
- [x] Returns HTTP 200 (all healthy) or 503 (any probe failed) with JSON detail
- [x] 7 unit tests covering probe success, timeout, ML-disabled, and partial failure

## Out of Scope

- Custom enrichment bundles (user-defined metric sets) — future phase
- Readiness probe for Kafka/SQS (no message bus in P1)
- Alert routing logic (handled by Alertmanager)
