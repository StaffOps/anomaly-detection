# Feature: SLO-Aware Severity Adjustment (P3.4)

## User Stories

WHEN an anomaly is detected for a service whose SLO error budget is above 80% remaining
THEN the system SHALL downgrade `warning` severity to `info`, reducing noise for on-call engineers when the service is healthy.

WHEN an anomaly is detected for a service whose SLO error budget is below 20% remaining
THEN the system SHALL upgrade `warning` severity to `critical`, maximizing attention when the budget is actively burning.

WHEN the SLO catalog has no entry for a given service
THEN the system SHALL leave severity unchanged (passthrough).

WHEN severity is adjusted
THEN the alert annotation SHALL include original severity, adjusted severity, and error budget remaining percentage.

## Acceptance Criteria

- [ ] SLO catalog loaded from config YAML at startup (service → budget query mapping)
- [ ] Error budget remaining queried from Prometheus-compatible TSDB per service per evaluation cycle
- [ ] Severity adjusted per threshold bands: `>80%` budget → downgrade, `<20%` budget → upgrade
- [ ] Threshold bands configurable (not hardcoded 80/20)
- [ ] Annotations on adjusted alerts contain: `slo_original_severity`, `slo_adjusted_severity`, `slo_budget_remaining_pct`
- [ ] Services not in catalog pass through with unmodified severity
- [ ] Query failures for a service result in passthrough (no adjustment), not pipeline failure
- [ ] Metrics emitted: adjustments count by direction (upgrade/downgrade/passthrough), query latency, query errors

## Out of Scope

- Defining SLOs or SLO targets (separate concern — owned by SRE)
- Creating or modifying SLO recording rules in the cluster
- Alertmanager routing changes
- Auto-discovery of SLOs from PrometheusRule labels (future enhancement, not P3.4)
