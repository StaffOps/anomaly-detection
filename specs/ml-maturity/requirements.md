# Feature: ML Maturity (Phase 2)

Covers P2.1 (ML multivariate fix) and P2.4 (workload-aware correlation).

## User Stories

### P2.1 — Proper Feature Vector for ML Multivariate

WHEN a correlated alert completes enrichment THEN the system SHALL build a stable, named feature vector from the enrichment bundle (5-8 distinct features: cpu_ratio, memory_ratio, restarts, error_rate, latency, etc.) and run ML multivariate detection against it.

WHEN ML confirms an anomaly (score above threshold) THEN the system SHALL auto-escalate the alert severity from warning to critical and annotate with `ml_score`, `ml_features`, `ml_contributors`.

WHEN building the feature vector THEN the system SHALL combine the triggering anomaly metric with enrichment results, ensuring distinct named features (fixing the same-metric-collision pattern from 0.6.0).

### P2.4 — Workload-Aware Correlation (Sibling Check)

WHEN ≥3 sibling pods of the same workload exhibit anomalies within the correlation window THEN the system SHALL emit 1 workload-level alert and suppress the contributing pod-level alert groups.

WHEN extracting workload identity THEN the system SHALL use regex patterns matching Deployment (`<name>-<rs_hash>-<pod_hash>`), StatefulSet (`<name>-<N>`), and DaemonSet (`<name>-<5-char>`) naming conventions.

## Acceptance Criteria

### P2.1
- [x] `internal/ml/features.go` builds stable feature vectors with 5-8 named features
- [x] ML runs per correlated alert post-enrichment (not pre-enrichment)
- [x] No same-metric-collision — each feature slot is distinct
- [x] Auto-escalation warning→critical on ML confirm
- [x] Annotations added: `ml_score`, `ml_features`, `ml_contributors`
- [x] Unit tests covering feature vector construction and escalation logic

### P2.4
- [x] `internal/correlation/workload.go` extracts workload from pod name via regex
- [x] Supports Deployment, StatefulSet, DaemonSet naming patterns
- [x] Emits workload-level alert when ≥N sibling pods anomaly (configurable, default 3)
- [x] Suppresses contributing pod-level alert groups
- [x] Config: `controller.workload_pattern_min_pods` (default 3)
- [x] Metrics exposed: `workload_patterns_total`, `pod_alerts_suppressed_total`
- [x] 15 unit tests covering extraction, pattern detection, suppression
- [ ] Validated in production (awaiting deploy)

## Out of Scope

- ML model retraining or online learning
- Workload extraction via K8s API (deliberately avoided)
- Cross-namespace workload correlation
- Custom workload naming patterns beyond Deployment/StatefulSet/DaemonSet
