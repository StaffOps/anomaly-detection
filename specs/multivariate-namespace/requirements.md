# Feature: Multivariate Per-Namespace Anomaly Detection (P2.3)

## User Stories

WHEN multiple services in the same namespace exhibit simultaneous anomalies THEN the system SHALL fire a namespace-level alert indicating a probable shared-dependency failure.

WHEN a namespace-level anomaly is detected THEN the system SHALL annotate the alert with the list of affected services so that the SRE can immediately assess blast radius.

WHEN a platform engineer receives a namespace alert THEN they SHALL see which detectors triggered and the aggregate severity, enabling rapid triage toward the shared resource (DB, cache, network).

WHEN a namespace-level pattern fires THEN the system SHALL suppress individual per-service alerts for the same namespace/window to avoid alert fatigue.

## Acceptance Criteria

- [ ] Namespace aggregation runs once per detection cycle, after individual anomaly detection completes
- [ ] Alert fires when `distinct_services_affected >= min_services` (configurable, default 3) within the aggregation window
- [ ] Alert annotations include: affected service names, pod count, max severity, triggering detectors
- [ ] Alert links to namespace dependency graph (when service-dependency-graph is available)
- [ ] Individual alerts are suppressed when a namespace-level pattern fires for the same window
- [ ] Suppression is reversible (if namespace alert is resolved, individual alerts resume)
- [ ] Metrics exposed: `namespace_anomaly_detected_total`, `namespace_anomaly_services_affected`
- [ ] Configuration supports per-namespace overrides for `min_services` threshold

## Out of Scope

- Identifying **which** shared dependency failed (covered by `service-dependency-graph` spec)
- Cross-namespace correlation (covered by `cross-namespace-correlation` if planned)
- Automated remediation of shared dependencies
