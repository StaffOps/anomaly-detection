# Service Dependency Graph — Requirements

## Context

Today the anomaly-detection system sees each service in isolation. When an
anomaly fires on service B, the operator has no automated way to know that B
depends on service A (which is also degraded) — or that B is a dependency of
C (which will degrade next). The operator pieces this together manually via
Tempo traces, Grafana service maps, and tribal knowledge.

This spec introduces a **service dependency graph** that:
1. Discovers communication paths between services automatically (from traces)
2. Correlates anomalies across the graph (propagation detection)
3. Exposes the graph for visualization (Grafana Node Graph panel)

This is ROADMAP item P2.6 — cross-workload dependency mapping.

## User Stories

### As an on-call SRE receiving an alert

- WHEN service B fires an anomaly THEN the alert annotations SHALL include
  its upstream dependencies (who B calls) and downstream dependents (who
  calls B), so I know where to look first.
- WHEN multiple services in a dependency chain are anomalous simultaneously
  THEN the system SHALL identify the **root** (earliest anomaly in the chain)
  and annotate downstream alerts as "likely propagation from {root}".

### As a platform engineer using Grafana

- WHEN I open the service dependency dashboard THEN I SHALL see a node graph
  showing all services, their connections (edges), and which nodes are
  currently anomalous (color-coded by severity).
- WHEN I click a node THEN I SHALL navigate to the service's detail panel
  (metrics, traces, logs).
- WHEN I hover an edge THEN I SHALL see the request rate, error rate, and
  latency between the two services.

### As a capacity planner

- WHEN I query the dependency graph THEN I SHALL see which services are
  "hubs" (many dependents) vs "leaves" (no dependents), so I can prioritize
  reliability investments on high-fan-in services.

### As an incident responder

- WHEN an incident involves N services THEN the graph SHALL highlight the
  affected subgraph and suggest the propagation order based on anomaly
  timestamps, reducing MTTD (mean time to diagnose).

## Acceptance Criteria

### Graph discovery
- [ ] Service-to-service edges extracted from Tempo `service_graph` metrics
      (or `traces_service_graph_request_total` if available)
- [ ] Graph refreshed periodically (configurable, default every 5 min)
- [ ] Graph stored in Redis as adjacency list (TTL-based expiry for stale edges)
- [ ] Services identified by `service.name` (same key as anomaly identity)

### Anomaly propagation
- [ ] When anomaly fires, controller looks up the service in the graph
- [ ] Upstream/downstream neighbors checked for concurrent anomalies (within
      configurable window, default 5 min)
- [ ] If upstream is also anomalous AND its anomaly started earlier → annotate
      current alert with `propagation_source: {upstream}` and
      `propagation_confidence: {score}`
- [ ] Root identification: walk upstream until no further anomalous ancestor
      → that's the likely root cause service

### Visualization (Grafana Node Graph)
- [ ] Prometheus-compatible metrics exposed in format compatible with Grafana Node
      Graph panel (node + edge metrics)
- [ ] Node metric: `staffops_ad_graph_node_state{service}` (0=healthy,
      1=warning, 2=critical)
- [ ] Edge metrics: `staffops_ad_graph_edge_requests_total{source,target}`,
      `staffops_ad_graph_edge_errors_total{source,target}`,
      `staffops_ad_graph_edge_duration_seconds{source,target}`
- [ ] Grafana dashboard with Node Graph panel showing live topology + health
- [ ] Color coding: green=healthy, yellow=warning, red=critical, grey=unknown
- [ ] Edge thickness proportional to request rate

### Alert enrichment
- [ ] Alert annotations include `dependencies` (upstream list) and
      `dependents` (downstream list)
- [ ] Alert annotations include `propagation_chain` when detected
      (ordered list: root → ... → this service)
- [ ] Link to Grafana Node Graph panel filtered to affected subgraph

## Out of Scope

- Cross-cluster dependencies (future — requires multi-cluster graph merge)
- Automatic remediation based on graph (human decides, system informs)
- Building a custom UI (Grafana Node Graph panel is the interface)
- Replacing Tempo service map (complementary — we consume it, not replace)
- Real-time streaming graph updates (periodic refresh is sufficient)

## Dependencies

- Tempo with `service_graph` processor enabled (generates
  `traces_service_graph_request_total`, `*_errors_total`,
  `*_request_server_seconds`)
- OR Prometheus-compatible TSDB already scraping service graph metrics from
  the OTel Collector `servicegraph` connector
- Redis (already available — used for graph storage)
- Grafana ≥ 9.0 (Node Graph panel GA)

## Cross-references

- ROADMAP: P2.6 (cross-workload dependency mapping)
- Related: P2.4 workload-aware correlation (intra-workload) — this spec is
  inter-workload
- Tempo service_graph: already configured in OTel Collector pipeline
- Grafana Node Graph panel docs: native visualization for this type of data
