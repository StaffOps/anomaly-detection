# Design: Observability Hardening (Phase 4)

## Architecture

```
Controller (Go)
├── internal/metrics/       ← Prometheus registry + constLabels wrapper
├── internal/otel/          ← otel-helper-go integration + slog bridge
├── cmd/controller/main.go  ← gRPC server with OTel interceptors
└── cmd/worker/main.go      ← gRPC client with OTel interceptors

Telemetry flow:
  Metrics: App → Prometheus /metrics → vmagent scrape → Prometheus-compatible TSDB
  Traces:  App → OTel SDK → OTel Collector (gRPC) → Tempo
  Logs:    slog → OTel bridge → OTel Collector → Loki
```

## Components

| Component | Responsibility |
|-----------|----------------|
| `internal/metrics` | Registry with constLabels, bounded labels, custom buckets |
| `internal/otel` | OTel Helper init, slog bridge, graceful fallback |
| gRPC interceptors | Distributed tracing across controller↔worker |
| Grafana dashboard JSON | 18 panels on `staffops_ad_*` + cardinality watch |

## Rationale

### Decision 1: constLabels for cluster identity (not externalLabels in app)

**Choice**: Wrap Prometheus registry with `constLabels{cluster: cfg.Cluster}`.

**Justification, in order of strength**:
1. Local dev (docker-compose) self-scrapes — no vmagent to inject externalLabels, so metrics would lack cluster identity entirely
2. Per `observability-principles` steering, apps only set `service.name` as resource attribute — but Prometheus metrics are a separate path from OTel resource attributes
3. In production, vmagent externalLabels adds `cluster` + `eks_cluster` at scrape time; the constLabel is idempotent (same value, no conflict)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Slightly redundant label in prod (app + vmagent both set `cluster`) | vmagent deduplicates; no cardinality impact |
| App must know its cluster name via config | Already needed for Alertmanager annotations |

**When this decision would be wrong**:
- If cluster name were dynamic/unknown at startup (it's not — comes from env var)

### Decision 2: No identity labels on metrics

**Choice**: Pod identity (pod name, IP, instance) NEVER appears as a Prometheus label. Identity goes to Alertmanager annotations only.

**Justification, in order of strength**:
1. Cardinality: `pod_name` × N metrics × M label combos = unbounded series growth on every rollout
2. Identity is useful for alert routing (annotations), not for aggregation (labels)
3. `workload` label (extracted via `ExtractWorkload()`) provides the bounded grouping needed for dashboards

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Cannot filter metrics by specific pod in PromQL | Use traces or logs for pod-level drill-down |
| Alertmanager annotations not queryable in metrics TSDB | Acceptable — annotations are for humans, not queries |

**When this decision would be wrong**:
- If per-pod metric comparison were a primary use case (it's not — USE method applies at workload level)

### Decision 3: OTel Helper lib over manual SDK setup

**Choice**: Integrate `staffops/otel-helper-go` instead of configuring OTel SDK manually.

**Justification, in order of strength**:
1. Corporate standard: enforces `AlwaysOnSampler`, correct resource attributes, full instrumentation set — per `observability-principles` steering
2. Graceful fallback (no-op when Collector unreachable) built into the helper — no custom retry logic needed
3. Reduces boilerplate: one `otel.Init()` call vs 30+ lines of manual provider/exporter/propagator setup

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Dependency on internal lib (not OSS) | Owned by same team; versioned; Go module |
| Less control over individual exporter options | Helper exposes config struct for overrides when needed |

**When this decision would be wrong**:
- If the service needed non-standard sampling (it doesn't — AlwaysOnSampler + Collector tail sampling is the pattern)

## Invariants

- No metric label with unbounded cardinality (enforced by `ExtractWorkload()` and no identity labels)
- OTel SDK failure MUST NOT crash the application (graceful fallback)
- All gRPC calls between controller and worker produce spans (interceptors mandatory)
- Dashboard panels MUST use `staffops_ad_*` prefix (no legacy metric names)

## Dependencies

| Service | Purpose |
|---------|---------|
| `staffops/otel-helper-go` | OTel SDK initialization + slog bridge |
| OTel Collector (cluster) | Trace/log export target |
| Prometheus-compatible TSDB | Metrics storage (scraped by agent) |
| Grafana | Dashboard rendering |
