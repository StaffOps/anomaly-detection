# Service Dependency Graph — Tasks

> **Status**: `FUTURE` — Spec written, blocked on Phase 0 outcome + production-hardening (P5)

## Phase 1: Graph discovery (`internal/graph/`)

- [ ] T1: Create `Edge` and `Graph` structs + `Config` with parsing YAML
      (`graph.enabled`, `graph.refresh_interval`, `graph.edge_ttl`,
      `graph.max_depth`)
- [ ] T2: Implement `Discoverer` — periodic PromQL query for
      `traces_service_graph_request_total`, builds edge list with rate/errors/latency
      per edge (depends on: T1)
- [ ] T3: Implement `AdjacencyStore` — Redis hash storage for edges, TTL-based
      expiry, methods: `SetEdges`, `GetNeighbors(service, direction)`,
      `GetFullGraph` (depends on: T1)
- [ ] T4: Wire `Discoverer` into controller boot — separate goroutine with own
      ticker, graceful shutdown, no-op when `graph.enabled=false` (depends on: T2, T3)
- [ ] T5: Add metrics: `staffops_ad_graph_discovery_duration_seconds`,
      `staffops_ad_graph_nodes_total`, `staffops_ad_graph_edges_total`
      (depends on: T4)

## Phase 2: Propagation detection

- [ ] T6: Implement `Propagator.CheckPropagation(anomaly)` — looks up service
      in graph, checks upstream neighbors for concurrent anomalies within window
      (depends on: T3)
- [ ] T7: Implement temporal ordering logic — compare anomaly start timestamps,
      score propagation confidence (depends on: T6)
- [ ] T8: Implement graph walk for root identification — walk upstream (max depth)
      until no further anomalous ancestor (depends on: T7)
- [ ] T9: Wire into correlator — after anomaly detection, before dispatch, call
      `Propagator` and attach results to anomaly struct (depends on: T8)
- [ ] T10: Add annotations to alert payload: `propagation_source`,
      `propagation_chain`, `propagation_confidence`, `dependencies`,
      `dependents` (depends on: T9)

## Phase 3: Metrics for Grafana Node Graph

- [ ] T11: Implement `MetricsExporter` — exposes `staffops_ad_graph_node_state`
      gauge (updated every cycle based on active anomalies) (depends on: T3)
- [ ] T12: Expose edge metrics: `staffops_ad_graph_edge_requests_total`,
      `staffops_ad_graph_edge_errors_total`, `staffops_ad_graph_edge_duration_seconds`
      (populated from discovery data) (depends on: T2)
- [ ] T13: Validate cardinality: confirm edge count stays within bounds
      (services² < 2500 for 50 services — acceptable) (depends on: T12)

## Phase 4: Grafana dashboard

- [ ] T14: Create Node Graph panel JSON (nodes query + edges query + color
      mapping + click-through links) (depends on: T11, T12)
- [ ] T15: Add "Affected Subgraph" panel variant — filtered by namespace or
      current incident services (depends on: T14)
- [ ] T16: Add link to Node Graph in alert annotations (deep-link with service
      filter pre-applied) (depends on: T14)

## Phase 5: Tests

- [ ] T17: Unit tests for `Discoverer` — mock PromQL response, verify edge
      extraction and deduplication (depends on: T2)
- [ ] T18: Unit tests for `AdjacencyStore` — miniredis, verify TTL expiry,
      neighbor lookup both directions (depends on: T3)
- [ ] T19: Unit tests for `Propagator` — mock graph + mock anomaly store,
      verify propagation scoring and root identification (depends on: T8)
- [ ] T20: Integration test via replay — run replay on window with known
      cascading failure, verify propagation chain matches expected order
      (depends on: T9)
