# Design: SLO-Aware Severity Adjustment

## Pipeline Position

```
Detection → Enrichment → [SLO Severity Adjuster] → Suppression → Dispatch
```

The adjuster sits after detection (anomaly exists with initial severity) and before dispatch (severity is still mutable). This is a transform stage, not a filter.

## Components

| Component | Responsibility |
|-----------|---------------|
| SLO Catalog | Config YAML loaded at startup; maps `service_name` → PromQL query for budget remaining |
| Budget Querier | Executes PromQL against Prometheus-compatible TSDB, returns `float64` (0.0–1.0) |
| Severity Adjuster | Applies threshold logic to mutate severity + add annotations |
| Pipeline Integration | Wires adjuster into the anomaly pipeline between enrichment and dispatch |

## SLO Budget Query

Standard recording rule pattern for error budget remaining:

```promql
1 - (rate(errors_total{service="$service"}[30m]) / rate(requests_total{service="$service"}[30m])) / (1 - $target)
```

The catalog stores the concrete query per service (pre-rendered or templated). The controller executes it as an instant query and reads the scalar result.

## Catalog Format

```yaml
slo:
  query_timeout: 5s
  thresholds:
    downgrade_above: 0.80  # budget > 80% → warning becomes info
    upgrade_below: 0.20    # budget < 20% → warning becomes critical
  services:
    - name: payments-api
      budget_query: "slo:error_budget_remaining:ratio{service='payments-api'}"
    - name: orders-api
      budget_query: "slo:error_budget_remaining:ratio{service='orders-api'}"
```

## Adjustment Logic

```
budget = query(service.budget_query)

if budget > thresholds.downgrade_above:
    severity = max(severity - 1, info)       # downgrade
elif budget < thresholds.upgrade_below:
    severity = min(severity + 1, critical)   # upgrade
else:
    severity = severity                      # passthrough
```

Severity levels: `info < warning < critical`. Only one step per adjustment (no `info → critical` jump).

## Rationale

### Decision 1: Compute at detection time, not in Alertmanager

**Choice**: Severity adjustment happens in the controller pipeline, not via Alertmanager routing.

**Justification**:
1. Alertmanager routes are static YAML — they cannot query live TSDB state at routing time
2. Dynamic severity requires a live budget value computed per-evaluation, which only the controller can do
3. Keeps Alertmanager config simple and predictable (routes by label, not by computed value)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Extra PromQL query per service per cycle | Bounded by catalog size (tens, not thousands); instant queries are cheap |
| Controller becomes severity-aware | Acceptable — controller already owns anomaly lifecycle |

**When this decision would be wrong**:
- If Alertmanager gains native query-time evaluation (unlikely in current architecture)
- If catalog grows to 500+ services (would need batching/caching)

### Decision 2: Config YAML catalog over auto-discovery

**Choice**: Explicit YAML mapping of service → SLO query.

**Justification**:
1. Predictable — operator controls exactly which services participate
2. No dependency on VMRule label conventions being consistent
3. Simpler to implement and debug

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Manual maintenance of catalog | Acceptable for <50 services; auto-discovery is future enhancement |

**When this decision would be wrong**:
- If SLO recording rules adopt a universal label convention AND catalog exceeds 30 services manually maintained

## Invariants

- Query failure for a service → passthrough (never fail the pipeline)
- Services not in catalog → passthrough (never block unknown services)
- Severity only moves one step per adjustment (no double-jump)
- Original severity always preserved in annotation (auditable)

## Dependencies

| Service | Purpose |
|---------|---------|
| Prometheus-compatible TSDB | Source of error budget remaining values |
| SLO recording rules | Must exist in cluster for budget queries to return data |
