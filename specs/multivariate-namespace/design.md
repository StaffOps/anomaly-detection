# Design: Multivariate Per-Namespace Anomaly Detection (P2.3)

## Architecture

```
Individual Anomaly Detection (per-service)
        │
        ▼
┌─────────────────────────┐
│  Namespace Aggregator   │  ← groups anomalies by namespace per cycle
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────┐
│  Threshold Check        │  ← distinct_services >= min_services?
└────────────┬────────────┘
             │ (if met)
             ▼
┌─────────────────────────┐
│  ML Multivariate Call   │  ← Isolation Forest on feature vector
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────┐
│  Suppression Logic      │  ← suppress individual alerts for namespace
└────────────┬────────────┘
             │
             ▼
        Alert Dispatch
```

## Components

| Component | Responsibility |
|-----------|----------------|
| Namespace Aggregator | Collects per-cycle anomalies, groups by `k8s.namespace.name` |
| Threshold Gate | Evaluates `min_services` before invoking ML (avoids unnecessary calls) |
| ML Multivariate | Isolation Forest on feature vector to confirm correlated failure |
| Suppression Controller | Inhibits individual alerts when namespace pattern fires |

## Rationale

### Decision 1: Namespace as aggregation unit

**Choice**: Aggregate anomalies by Kubernetes namespace.

**Justification**:
1. Namespace = team/domain boundary in BDC — shared resources (DB, Redis, ConfigMaps) are typically scoped to namespace
2. RBAC and network policies already segment by namespace — blast radius is bounded
3. Labels like `k8s.namespace.name` are already present on all telemetry (zero extra enrichment)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Cross-namespace shared deps (e.g., central Redis) not caught | Covered by future cross-namespace spec |
| Large namespaces with unrelated services may false-positive | Mitigated by `min_services` threshold + ML confirmation |

**When wrong**: if teams move to shared namespaces (multi-tenant namespace pattern).

### Decision 2: Threshold gate before ML

**Choice**: Only invoke ML multivariate when `distinct_services >= min_services` (default 3).

**Justification**:
1. ML calls are expensive — avoid on every cycle for every namespace
2. 1-2 anomalous services is normal noise; 3+ is signal worth investigating
3. Configurable per-namespace for namespaces with fewer total services

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Namespace with only 2 services never triggers | Per-namespace override to `min_services=2` |

**When wrong**: if ML could detect subtle 2-service correlations that threshold misses.

### Decision 3: Suppress individual alerts on namespace pattern

**Choice**: When namespace-level alert fires, suppress individual per-service alerts within the same window.

**Justification**:
1. Receiving N individual alerts + 1 namespace alert is noise — the namespace alert subsumes them
2. Same pattern as workload-collapse (P2.4) — consistent UX
3. Suppression is reversible: if namespace alert resolves, individual alerts resume

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| If ML false-positives the namespace, individual alerts are lost | Suppression window is bounded; individual anomalies still recorded in TSDB |

**When wrong**: if operators prefer to see both levels simultaneously (make suppression configurable).

### Decision 4: Feature vector for multivariate

**Choice**: `{anomalous_pod_count, distinct_services, max_severity, distinct_detectors}`

**Justification**:
1. Pod count captures scale of impact
2. Distinct services captures breadth (shared-dep signal)
3. Max severity captures worst-case urgency
4. Distinct detectors captures signal diversity (multiple detection methods agreeing = high confidence)

**When wrong**: if temporal spread matters (all at once vs rolling — may need `time_spread_seconds` later).

## Invariants

- Namespace aggregation MUST run after all individual detections for the cycle complete
- Suppressed individual alerts MUST still be recorded as anomalies in the TSDB
- Namespace alert MUST include full list of affected services (not just count)

## Dependencies

| Service | Purpose |
|---------|---------|
| anomaly-detection-controller | Provides per-cycle individual anomaly results |
| anomaly-detection-ml | Isolation Forest multivariate confirmation |
| Alertmanager | Dispatches namespace-level alerts |
| Prometheus-compatible TSDB | Stores namespace anomaly metrics |
