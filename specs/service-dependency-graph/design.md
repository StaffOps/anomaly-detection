# Service Dependency Graph — Design

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Data Sources                           │
├─────────────────────────────────────────────────────────┤
│  Tempo service_graph metrics (via Prometheus-compatible TSDB) │
│  traces_service_graph_request_total{client,server}      │
│  traces_service_graph_request_server_seconds{...}       │
│  traces_service_graph_request_failed_total{...}         │
└──────────────────────────┬──────────────────────────────┘
                           │ periodic poll (PromQL)
                           ▼
┌─────────────────────────────────────────────────────────┐
│            internal/graph/                               │
├─────────────────────────────────────────────────────────┤
│  Discoverer     → polls service_graph metrics           │
│  AdjacencyStore → Redis hash (edges + metadata)         │
│  Propagator     → walks graph on anomaly, scores chain  │
│  MetricsExporter→ exposes node/edge state for Grafana   │
└──────────────────────────┬──────────────────────────────┘
                           │
              ┌────────────┼────────────────┐
              ▼            ▼                ▼
     Correlator      Alert Enrichment   Grafana
     (propagation    (annotations:      (Node Graph
      detection)      chain, deps)       panel)
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `Discoverer` | Periodically queries VM for service_graph metrics, builds/updates graph |
| `AdjacencyStore` | Redis-backed graph storage (adjacency list, edge weights, TTL) |
| `Propagator` | On anomaly: walks graph, finds concurrent anomalies, scores propagation |
| `MetricsExporter` | Exposes `staffops_ad_graph_*` metrics for Grafana Node Graph |

## Data Model

### Graph in Redis

```
Key: graph:edges:{service_a} → Hash {
  "service_b": "{rate: 120, error_rate: 0.02, p99: 0.45, last_seen: ts}",
  "service_c": "{rate: 45, ...}",
}
Key: graph:meta → Hash {
  "last_refresh": "2026-06-22T12:00:00Z",
  "node_count": "47",
  "edge_count": "128",
}
TTL: edges expire if not refreshed within 2× refresh interval (stale = service stopped communicating)
```

### Edge discovery query (PromQL)

```promql
# All edges with rate > 0 in last 15 min
sum by (client, server) (
  rate(traces_service_graph_request_total[15m])
) > 0
```

### Propagation scoring

```
score = temporal_proximity × edge_weight × severity_factor

temporal_proximity: 1.0 if upstream anomaly started first (within window)
                   0.5 if simultaneous (within 30s)
                   0.0 if downstream started first

edge_weight: normalized request_rate / max_request_rate (high traffic = likely path)

severity_factor: 1.0 if upstream is critical, 0.7 if warning, 0.3 if info
```

## Rationale

### Decision 1: Tempo service_graph as primary source (not span analysis)

**Choice**: Consume pre-computed `service_graph` metrics from the OTel Collector
`servicegraph` connector, not analyze raw spans.

**Justification**:
1. Service graph metrics already exist in the Prometheus-compatible TSDB (zero new infra)
2. Pre-aggregated by the Collector — no trace storage scanning needed
3. Sub-second PromQL query vs seconds/minutes for Tempo search
4. Works in replay mode (metrics are in VM history)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Loses per-endpoint granularity (only service-level edges) | Sufficient for dependency mapping; endpoint detail is in Tempo UI |
| Depends on Collector having servicegraph connector | Already configured in BDC OTel Collector pipeline |

**When this decision would be wrong**:
- If service_graph metrics are not available or disabled
- If per-endpoint dependency mapping is needed (e.g., `/api/orders` calls service B but `/api/users` doesn't)

### Decision 2: Redis adjacency list (not in-memory graph)

**Choice**: Store graph in Redis, not in controller memory.

**Justification**:
1. Survives controller restarts without cold-start (graph is expensive to rebuild)
2. Shared across controller replicas (leader election — follower can read graph)
3. TTL-based expiry handles service decommissioning automatically
4. Consistent with existing Redis usage (baselines, dedup, enrichment cache)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Redis latency on graph walk (~1-5ms per hop) | Acceptable — propagation check runs once per anomaly, not per tick |
| Redis memory | ~50 bytes per edge × 200 edges = ~10KB (negligible) |

**When this decision would be wrong**:
- If graph has >10K edges (unlikely — bounded by service count)
- If sub-millisecond graph traversal is needed (not the case)

### Decision 3: Grafana Node Graph panel (not custom UI)

**Choice**: Expose metrics in format compatible with Grafana Node Graph panel.
No custom frontend.

**Justification**:
1. Zero frontend development (anti-goal: we don't build UIs)
2. Native Grafana panel — operators already know the tool
3. Supports click-through to other panels (traces, logs, metrics)
4. Color coding, filtering, search built in

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Limited layout control (Grafana auto-arranges) | Acceptable for topology view |
| Requires specific metric format (nodes table + edges table) | Well-documented, achievable with recording rules |

**When this decision would be wrong**:
- If stakeholders need a dedicated topology product with custom UX
- If graph needs to show >500 nodes (Grafana panel degrades)

### Decision 4: Propagation via temporal ordering (not causal inference)

**Choice**: Determine propagation direction by "who anomalied first" + graph
edge direction, not by statistical causal inference (Granger, etc.).

**Justification**:
1. Simple, interpretable, debuggable
2. Edge direction from traces already encodes call direction (A calls B)
3. Combined with temporal ordering: if A anomalied before B AND A→B edge exists
   → high confidence A propagated to B
4. No model training needed

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Can't detect reverse causation (B slow → A backs up waiting) | Annotate as "correlation" not "causation" — operator decides |
| Simultaneous anomalies get lower confidence | Correct behavior — genuinely ambiguous |

**When this decision would be wrong**:
- If the system needs to distinguish "A caused B" from "shared upstream C caused both"
  with statistical rigor (Granger causality, PC algorithm)

## Invariants

- Graph discovery MUST NOT affect detection cycle performance (runs on
  separate goroutine with own ticker)
- Edge metrics MUST be bounded cardinality: source × target ≤ service_count²
  (typically ~200 edges for 50 services)
- Propagation walk MUST have max depth (default 5) to prevent infinite loops
  on circular dependencies
- Node Graph metrics MUST use the same `service.name` identity as anomaly
  detection (otherwise join breaks)

## Grafana Node Graph Integration

The Node Graph panel requires two "table" datasources:

**Nodes table** (recording rule or instant query):
```promql
# One row per service with current state
staffops_ad_graph_node_state{service="svc-a"} → 0 (healthy) / 1 (warning) / 2 (critical)
```

**Edges table** (recording rule):
```promql
# One row per edge with traffic metadata
staffops_ad_graph_edge_requests_total{source="svc-a", target="svc-b"}
staffops_ad_graph_edge_errors_total{source="svc-a", target="svc-b"}
```

Dashboard JSON template will use `type: nodeGraph` panel with these queries.
