# Tasks: Observability Hardening (Phase 4)

> **Status**: `DONE` — All items completed in controller 0.7.0 (2026-05-30)

- [x] Task 1: Fix `alerts_fired_total` — increment before dryRun check to measure intent
- [x] Task 2: Fix `workers_available` — set gauge per tick via `GetState()`
- [x] Task 3: Fix `cycle_duration_seconds` — custom buckets [1, 2.5, 5, 10, 20, 30, 60]
- [x] Task 4: Remove identity labels from all counters/histograms
- [x] Task 5: Restrict `AlertsFired` to `[severity]` label only
- [x] Task 6: Move pod identity to Alertmanager annotations
- [x] Task 7: Implement `ExtractWorkload()` for bounded workload label
- [x] Task 8: Wrap Prometheus registry with `constLabels{cluster: cfg.Cluster}`
- [x] Task 9: Remove `eks_cluster` from app-emitted metrics
- [x] Task 10: Rewrite 18 dashboard panels to `staffops_ad_*` taxonomy
- [x] Task 11: Add cardinality watch panel to dashboard
- [x] Task 12: Integrate `staffops/otel-helper-go` (depends on: Task 8)
- [x] Task 13: Add gRPC OTel interceptors on controller↔worker
- [x] Task 14: Implement slog→OTel logs bridge
- [x] Task 15: Add graceful fallback when OTel Collector unavailable
