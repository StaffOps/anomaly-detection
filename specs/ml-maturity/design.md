# Design: ML Maturity (Phase 2)

## Architecture

```
Anomaly detected (worker)
  → Enrichment bundle (parallel queries: CPU, memory, restarts, error rate, latency)
    → Feature vector construction (internal/ml/features.go)
      → ML multivariate detection (gRPC call to ml service)
        → Escalation + annotation (if confirmed)
          → Correlation window
            → Workload pattern check (internal/correlation/workload.go)
              → Workload-level alert OR individual pod alerts
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `internal/ml/features.go` | Build named feature vectors from enrichment results |
| `internal/ml/escalation.go` | Auto-escalate severity on ML confirmation |
| `internal/correlation/workload.go` | Extract workload identity, detect sibling patterns |
| `internal/correlation/suppression.go` | Suppress pod-level alerts when workload pattern fires |

## Rationale

### Decision 1: ML runs post-enrichment

**Choice**: ML multivariate detection executes after the enrichment bundle completes, not at initial anomaly detection time.

**Justification, in order of strength**:
1. Pre-enrichment, only the triggering metric is available — building a "multivariate" vector from one metric caused same-metric-collision (the 0.6.0 bug where all feature slots contained the same value)
2. Enrichment provides 5-8 genuinely distinct signals (CPU, memory, restarts, error rate, latency) — the diversity ML needs to be useful
3. Post-enrichment ordering guarantees the feature vector is complete before ML inference

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Higher latency to ML result (waits for enrichment) | Enrichment is ~500ms; acceptable for non-real-time alerting |
| ML cannot prevent enrichment cost for false positives | Enrichment is cheap (cached baselines + single queries) |

**When this decision would be wrong**:
- If enrichment latency exceeds alert SLO (>5s)
- If ML needs to run on raw signal before any processing (streaming ML)

### Decision 2: Workload extraction via regex (not K8s API)

**Choice**: Extract workload identity from pod name using regex patterns for known controllers (Deployment, StatefulSet, DaemonSet).

**Justification, in order of strength**:
1. No runtime K8s API dependency — controller works in offline/replay mode without kubeconfig
2. Deterministic: same pod name always yields same workload — no race with API cache
3. Standard K8s naming is stable and well-defined (hasn't changed since 1.0)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Custom naming (non-standard pod name generators) won't match | Rare in practice; standard controllers cover >99% of workloads at BDC |
| No owner reference validation | Regex matches are sufficient for correlation purposes |

**When this decision would be wrong**:
- If workloads use custom naming that breaks the standard patterns
- If the controller needs to distinguish two Deployments with same prefix but different owners

**Alternatives considered**:
- K8s API (`GET /apis/apps/v1/...`) — rejected: adds dependency, fails offline, latency

### Decision 3: Suppress pod-level alerts on workload pattern

**Choice**: When ≥N sibling pods from the same workload anomaly within the correlation window, emit 1 workload-level alert and suppress the N individual pod alerts.

**Justification, in order of strength**:
1. N identical alerts for the same root cause (Deployment-wide issue) creates noise — operators ignore alert floods
2. Single workload-level alert is actionable ("Deployment X is degraded") vs N pod alerts requiring mental correlation
3. Suppressed alerts are still recorded (metrics + annotations) — no data loss

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Per-replica issues (one pod leaking) get smoothed into workload alert | Mitigated: threshold is configurable; single-pod anomalies below threshold fire normally |
| Operator loses per-pod detail in the alert | Alert annotation lists all contributing pods; detail available in drill-down |

**When this decision would be wrong**:
- If operators need to act on individual pods differently (e.g., cordon one specific node)
- If threshold (default 3) is too low for very large Deployments (100+ replicas) — may need percentage-based threshold

## Invariants

- Feature vector MUST have ≥5 distinct named features before ML runs (fail-open: skip ML if enrichment incomplete)
- Workload pattern threshold MUST be ≥2 (1 pod is not a pattern)
- Suppressed pod alerts MUST still increment `pod_alerts_suppressed_total` metric
- ML escalation MUST be idempotent (running twice on same alert doesn't double-escalate)

## Dependencies

| Service | Purpose |
|---------|---------|
| ML service (gRPC :50051) | Isolation Forest multivariate detection |
| Redis | Enrichment cache, correlation window state |
| Prometheus-compatible TSDB | Enrichment queries (CPU, memory, latency, error rate) |
