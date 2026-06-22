# Tasks: ML Maturity (Phase 2)

> **Status**: `DONE` — Implemented in controller 0.7.0 (2026-05-30). P2.4 awaiting prod validation.

## P2.1 — Fix ML Multivariate (Proper Feature Vector)

- [x] Task 1: Create `internal/ml/features.go` with named feature vector builder
- [x] Task 2: Define feature slots (cpu_ratio, memory_ratio, restarts, error_rate, latency, pod_age, oom_signals, traffic_deviation)
- [x] Task 3: Wire feature builder to run post-enrichment (after enrichment bundle completes)
- [x] Task 4: Fix same-metric-collision — ensure each slot pulls from distinct enrichment result
- [x] Task 5: Implement auto-escalation warning→critical on ML score above threshold
- [x] Task 6: Add annotations to escalated alerts (ml_score, ml_features, ml_contributors)
- [x] Task 7: Unit tests for feature vector construction (edge cases: partial enrichment, missing metrics)
- [x] Task 8: Unit tests for escalation logic (idempotency, threshold boundary)

## P2.4 — Workload-Aware Correlation (Sibling Check)

- [x] Task 9: Create `internal/correlation/workload.go` with regex-based workload extractor
- [x] Task 10: Implement patterns for Deployment, StatefulSet, DaemonSet naming conventions
- [x] Task 11: Add workload pattern detection in correlator (≥N siblings within window)
- [x] Task 12: Implement pod-level alert suppression when workload pattern fires
- [x] Task 13: Emit workload-level alert with contributing pod list in annotations
- [x] Task 14: Add config `controller.workload_pattern_min_pods` (default 3)
- [x] Task 15: Expose metrics: `workload_patterns_total`, `pod_alerts_suppressed_total`
- [x] Task 16: Write 15 unit tests (extraction accuracy, pattern detection, suppression, config edge cases)
- [x] Task 17: Integration test with docker-compose stack (ML + correlation + workload suppression end-to-end)
