# Changelog

All notable changes to this project per [Keep a Changelog](https://keepachangelog.com/) and [Semantic Versioning](https://semver.org/).

Versioning is **milestone-based**, not commit-based. Each component (`controller/`, `ml/`) is versioned independently. See `staffops_agent_definition/steering/version-management.md` for the full policy.

---

## [Unreleased]

Work landed after controller 0.7.0, not yet released (still pre-production, no cluster deploy — no version bump per `version-management.md`).

### controller

**Fixed**
- `correlator.go`: `workloadKey()` now falls back to `service_name` when the pod label is empty. Service-level anomalies (latency/error-rate by service) no longer collapse into a single `/` group (was 348 anomalies in one bucket).

**Added — Replay mode (P3.1, 12/16 tasks)**
- `internal/replay/`: offline detection over historical metrics/logs with zero side effects (no Redis/Alertmanager/gRPC/ML).
  - Window parser (durations, `Nd` days, RFC3339, UTC), in-memory baseline store (Welford+EWMA), `baseline.Evaluator` interface
  - Tick simulator: 1h chunked range queries, dynamic warmup split, graceful per-tick query-error skip, SIGINT partial flush
  - Report serializers: JSON (full schema, `schema_version: 1`) + Markdown (tables + ASCII sparklines), both UTC
  - CLI: `--replay --from --to --output --warmup-fraction --max-range --max-anomalies` with VM/Loki/output pre-flight checks (ML is V2)
  - In-memory execution metrics embedded in `metadata.execution_metrics` (no Prometheus exposure in V1)
- Remaining: T13 integration test, T14 smoke test, T15 README, T16 ROADMAP move-to-Done.

### ml

**Fixed**
- `multivariate.py`: introduce `CANONICAL_FEATURES` (10 fixed features) with `_normalize()` padding. Eliminates the `ValueError: X has N features but model expects M` when pod-level (6+) and service-level (3-5) anomalies hit the same IsolationForest (was ~33% error rate).

---

## controller — [0.7.0] — 2026-05-30

Consolidated milestone covering a full day of MVP iteration. Multiple sub-features were implemented and validated together; they ship as a single release because none of them justify an independent bump on its own (still pre-production, dry-run only, no cluster deploy).

### Added

**Metrics taxonomy & build introspection**
- All Prometheus metrics renamed under `staffops_ad_*` with 5 sub-namespaces:
  - `staffops_ad_controller_*` — orchestration, leader, dispatch, cycles
  - `staffops_ad_worker_*` — query execution, baselines, Redis ops
  - `staffops_ad_detection_*` — anomalies detected (cross-cutting)
  - `staffops_ad_alert_*` — alert pipeline
  - `staffops_ad_ml_*` — ML service ops (Python side)
- `internal/version/version.go` SSOT and `staffops_ad_controller_build_info{version}` metric

**P1.1 — Label-based pivot (anomaly enrichment)**
- `internal/enrichment/` — identity extraction (pod/service kinds), template substitution (`$pod`, `$namespace`, `$service_name`, etc.), bounded concurrency, Redis-backed cache, multi-source (VM + Loki)
- Per-kind metrics: `staffops_ad_controller_enrichment_runs_total`, `_duration_seconds`, `_cache_hits_total`, `_cache_misses_total`, `_query_errors_total`
- Default `pod_bundle` (cpu_ratio, memory_ratio, restarts_5m, oom_kills, ready_replicas, error_logs_1m) and `service_bundle` (error_rate_1m, request_rate_1m, latency_p99_5m)
- Alert payload now ships with `enrich_*` annotations and a one-line `context` summary

**P1.2 — Alert payload with deep links**
- `internal/alert/links.go` `LinkBuilder` rendering Grafana Explore, Tempo TraceQL, Loki LogQL, and Runbook URLs into Alertmanager annotations
- New annotations: `grafana_url`, `tempo_url`, `loki_url`, `runbook_url`
- ±15min framing for metrics/traces, ±5min for logs, anchored at anomaly timestamp
- Per-detector runbook paths (`<base>/<detector>`)

**P1.3 — Complete readiness checks**
- `internal/readiness/` with checkers for VictoriaMetrics, Loki, Alertmanager, ML
- `/readyz` now probes all upstream dependencies (was Redis-only before)
- All probes capped at 3s
- ML probe is no-op when `ml.enabled=false`
- New metric `staffops_ad_controller_readiness_checks_total{dependency,result}`
- ML client gained `Health(ctx) error` and `Enabled() bool` methods

**P2.1 — ML multivariate proper feature vector**
- `internal/ml/features.go` `BuildFeatureVector(anomaly, enrichment.Bundle)` produces stable, named feature vectors (anomaly_score, anomaly_value, plus enrichment results)
- ML now runs **once per correlated alert** (post-enrichment), with 5-8 distinct features
- New `correlation.MLDetection` field on `CorrelatedAlert` carrying `IsAnomaly`, `Score`, `Contributors`, `FeatureCount`
- Auto-severity escalation: `warning` → `critical` when ML confirms
- New annotations: `ml_score`, `ml_features`, `ml_contributors`; new label `ml_confirmed=true`

**12-factor compliance — env-var-driven config**
- All endpoint URLs and environment-specific values driven by env vars
- `${VAR}` (required, fail-fast) and `${VAR:default}` (optional fallback) substitution in `config.yaml`
- Config loader expands placeholders BEFORE YAML parse, comment-aware (skips `#` lines)
- `.env.example` at repo root documenting every supported variable
- `docker-compose.yaml` passes env vars via YAML anchors (`x-app-env`)
- `.env` files gitignored

### Changed

- `correlation.NewCorrelator` signature now accepts an `Enricher` interface
- `alert.NewDispatcher` signature now accepts a `*LinkBuilder`
- `cmd/controller/main.go` flush loop uses `FireCorrelated` and runs ML per-alert
- Removed broken pre-flush `mlClient.DetectMultivariate(ctx, samples)` call (collapsed same-metric anomalies into single map key, defeating Isolation Forest)
- Removed default URLs from `setDefaults()` in `internal/config/config.go`

### Migration notes

- Operators must populate `.env` (or set env vars) before starting the stack. Required: `VM_URL`, `LOKI_URL`, `ALERTMANAGER_URL`. Stack fails fast if missing.
- Dashboards/alerts referencing the old `anomaly_*` metric names must update to `staffops_ad_*` taxonomy.
- ML invocation pattern changed (per-alert with feature vector, vs per-cycle with raw values). Operators may see fewer total ML calls but each is meaningful.

### Tests

- 26 new unit tests across `internal/alert/links_test.go`, `internal/readiness/readiness_test.go`, `internal/config/expand_test.go`, `internal/ml/features_test.go`

### Not in this release (still on roadmap)

- Cluster deploy / removal of `--dry-run`
- K8s Lease leader election
- Prophet Forecast wiring (P2.2)
- Replay mode / feedback loop / SLO-aware severity

---

## controller — [0.6.0] — 2026-05-26

### Added
- Initial controller + workers + Redis-backed baselines
- Static, Adaptive (Z-Score), and Pattern detectors
- Correlation engine with Redis-backed dedup
- Alertmanager dispatcher (dry-run mode)
- Suppression filter (namespace-level + static-only)
- Config hot-reload watcher
- docker-compose stack
- Initial ML gRPC client integration (later refactored in 0.7.0)

---

## ml — [0.2.0] — 2026-05-30

### Added
- Custom Prometheus metrics under `staffops_ad_ml_*`:
  - `staffops_ad_ml_requests_total{method,status}`
  - `staffops_ad_ml_request_duration_seconds{method}`
  - `staffops_ad_ml_forecast_breach_predicted_total`
  - `staffops_ad_ml_forecast_input_size`, `_confidence`
  - `staffops_ad_ml_multivariate_anomalies_total`
  - `staffops_ad_ml_multivariate_input_size`, `_score`
  - `staffops_ad_ml_ready`
  - `staffops_ad_ml_model_version_info{version}`
- gRPC error handling — sets `INTERNAL` status code on exceptions

### Changed
- Removed legacy metrics `ml_requests_total` and `ml_request_duration_seconds` (replaced by `staffops_ad_ml_*`)

---

## ml — [0.1.0] — 2026-05-26

### Added
- gRPC server on port 50051 with `Forecast` (Prophet) and `DetectMultivariate` (Isolation Forest)
- Health endpoint
