# Design: Detection Core (Phase 1)

## Architecture

```
Anomaly Detected
      │
      ▼
┌─────────────┐     ┌───────────┐     ┌──────────────────┐
│  Enrichment │────▶│ Redis LRU │────▶│ Prometheus-compatible TSDB│
│  Engine     │     │ Cache     │     │ (live PromQL)     │
└─────────────┘     └───────────┘     └──────────────────┘
      │
      ▼
┌─────────────┐
│ LinkBuilder │──▶ Grafana Explore / Tempo / Loki / Runbook URLs
└─────────────┘
      │
      ▼
┌─────────────┐
│ Alert Fire  │──▶ Alertmanager (enriched payload + links)
└─────────────┘

/readyz ──▶ [Redis, VM, Loki, AM, ML] ──▶ 200/503
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `enrichment.Engine` | Extracts workload identity, queries metrics, caches results |
| `enrichment.IdentityExtractor` | Regex-based extraction of namespace/workload/kind from labels |
| `enrichment.Cache` | Redis SET/GET with TTL, bounded key space |
| `links.LinkBuilder` | Constructs time-anchored URLs for each observability backend |
| `health.ReadinessAggregator` | Runs per-dependency probes with independent timeouts |

## Rationale

### Decision 1: Enrichment in the controller, not Alertmanager templates

**Choice**: The controller queries the Prometheus-compatible TSDB for ratio metrics and injects them into the alert payload before firing.

**Justification**:
1. Alertmanager templates cannot execute live queries — they only interpolate labels already present in the alert
2. Ratios (cpu_ratio, memory_ratio) require instant PromQL against the metrics TSDB at anomaly time
3. Centralizing enrichment keeps Alertmanager config simple and stateless

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Controller latency increases per alert | Bounded by semaphore concurrency + 3s query timeout; sub-second in practice |
| Controller depends on metrics TSDB availability | Already a hard dependency for detection; readiness check covers it |

**When this decision would be wrong**:
- Alert volume exceeds controller's enrichment throughput (>1000 alerts/min sustained)
- Enrichment data becomes available as recording rules (pre-computed labels)

### Decision 2: Redis LRU cache for enrichment results

**Choice**: Cache enrichment query results in Redis with TTL (60s default), keyed by workload identity.

**Justification**:
1. Same workload may trigger multiple anomalies within one detection cycle — avoid redundant VM queries
2. Redis already a dependency (baselines, dedup) — no new infra
3. TTL ensures staleness is bounded; LRU eviction keeps memory bounded

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Stale enrichment data (up to TTL) | 60s staleness acceptable for context metrics |
| Extra Redis key space | Bounded by workload cardinality (~hundreds), not alert volume |

**When this decision would be wrong**:
- Enrichment requires real-time accuracy (sub-second freshness)
- Redis becomes a bottleneck (unlikely at current scale)

### Decision 3: Time-anchored links with most-specific labels

**Choice**: LinkBuilder constructs URLs using the most specific labels available (pod > workload > namespace) and anchors time windows at the anomaly timestamp.

**Justification**:
1. Most-specific labels produce the most useful queries (fewer results to sift through)
2. Fixed time anchoring (±15min metrics, ±5min logs) ensures the SRE sees the relevant window immediately
3. Graceful degradation: if pod label is missing, falls back to workload, then namespace

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Links may miss context outside the time window | SRE can manually widen; starting narrow is better than starting at "last 1h" |

### Decision 4: Readiness as dependency health aggregator

**Choice**: `/readyz` runs all probes in parallel with per-probe 3s timeout, returns 503 if any fails.

**Justification**:
1. K8s readiness semantics: pod should not receive traffic if it can't serve (missing dependency = can't detect)
2. Per-probe timeout prevents one slow dependency from blocking the entire check
3. ML probe as no-op when disabled avoids false-negative readiness when ML is intentionally off

## Invariants

- Enrichment MUST NOT block alert firing — on cache miss + query timeout, fire alert without enrichment
- Links MUST be valid URLs even when labels are partially missing (graceful degradation)
- Readiness MUST return within 3s regardless of probe count (parallel execution)
- Redis cache keys MUST include namespace+workload to avoid cross-workload collisions

## Dependencies

| Service | Purpose |
|---------|---------|
| Prometheus-compatible TSDB | Live PromQL queries for enrichment ratios |
| Redis | Enrichment cache + existing baseline storage |
| Alertmanager | Alert delivery target |
| Loki | Link target (LogQL URLs) |
| Tempo | Link target (TraceQL URLs) |
| Grafana | Link target (Explore URLs) |
| ML service | Optional dependency, readiness-probed |
