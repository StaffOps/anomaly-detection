# Feature: Self-Monitoring VMRules (P6.1)

## User Story

AS an SRE responsible for the anomaly-detection system
WHEN the system degrades (slow cycles, failing queries, ML errors, leader flapping)
THEN I SHALL be alerted via standard Alertmanager→Slack pipeline before user-facing impact occurs.

## Acceptance Criteria

- [ ] PrometheusRule resource (compatible with VMRule CRD) containing all rules below:

| Rule | Condition | For | Severity |
|------|-----------|-----|----------|
| AnomalyDetectionCycleSlow | cycle duration p99 > 30s | 5m | warning |
| AnomalyDetectionWorkerQueryErrors | worker query error rate > 10% | 5m | critical |
| AnomalyDetectionMLCallErrors | ML gRPC call error rate > 5% | 5m | warning |
| AnomalyDetectionReadinessCheckFailing | readiness checks failing | 2m | critical |
| AnomalyDetectionCycleGap | no detection cycle completed in > 90s | 2m | critical |
| AnomalyDetectionLeaderFlapping | leader transitions > 3 in 5min | 0s | warning |
| AnomalyDetectionBaselineCardinalityHigh | baseline series > 80% of cardinality guard limit | 5m | warning |

- [ ] Every rule has annotations: `summary`, `description`, `runbook_url`
- [ ] Every rule has labels: `severity`, `team: platform`
- [ ] Rules deployed as part of the Helm chart release (template in `templates/prometheusrule.yaml`)
- [ ] Rules validate cleanly with `promtool check rules`

## Out of Scope

- Runbook content (separate task, links point to placeholder URLs)
- Grafana dashboard for self-monitoring (separate spec)
- SLO-based burn rate alerts (future iteration)

## Notes

- PrometheusRule CRD is the vendor-agnostic name; VictoriaMetrics operator watches it via VMRule compatibility mode. Both work identically.
- Metric names depend on the controller/worker instrumentation already in place.
