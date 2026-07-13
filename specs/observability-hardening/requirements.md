# Feature: Observability Hardening (Phase 4)

## User Stories

### US-1: Observability Engineer

WHEN I scrape metrics from the anomaly-detection controller
THEN the system SHALL emit correctly-instrumented counters, histograms, and gauges with bounded cardinality
AND the system SHALL include cluster identity via constLabels without polluting per-series labels with pod identity.

### US-2: SRE using Grafana Dashboards

WHEN I open the anomaly-detection dashboard in Grafana
THEN the system SHALL present 18 panels using the `staffops_ad_*` metric taxonomy
AND the system SHALL include a cardinality watch panel to detect label explosions early.

### US-3: Developer Debugging with Traces

WHEN I investigate a detection cycle end-to-end
THEN the system SHALL provide distributed traces across controller↔worker gRPC calls via OTel SDK
AND the system SHALL bridge slog output to OTel logs for correlation
AND the system SHALL degrade gracefully when the OTel Collector is unavailable.

## Acceptance Criteria

- [x] P4.A.1: `alerts_fired_total` counter incremented before dryRun check (measures intent, not delivery)
- [x] P4.A.1: `workers_available` gauge set per tick via `GetState()`
- [x] P4.A.1: `cycle_duration_seconds` histogram uses custom buckets `[1, 2.5, 5, 10, 20, 30, 60]`
- [x] P4.A.2: No identity label on any counter/histogram
- [x] P4.A.2: `AlertsFired` metric uses only `[severity]` as label
- [x] P4.A.2: Full pod identity sent to Alertmanager annotations (not labels)
- [x] P4.A.2: `workload` label bounded via `ExtractWorkload()`
- [x] P4.A.3: Prometheus registry wrapped with `constLabels{cluster: cfg.Cluster}`
- [x] P4.A.3: `eks_cluster` excluded from app code (belongs at scrape layer)
- [x] P4.A.4: 18 dashboard panels rewritten to `staffops_ad_*` taxonomy
- [x] P4.A.4: Cardinality watch panel added
- [x] P4.A.5: `staffops/otel-helper-go` integrated
- [x] P4.A.5: gRPC interceptors on controller↔worker communication
- [x] P4.A.5: slog→OTel logs bridge active
- [x] P4.A.5: Graceful fallback when OTel Collector unavailable

## Out of Scope

- Tail-based sampling configuration (Collector-side, not app responsibility)
- Alertmanager routing rules (separate concern)
- Dashboard provisioning automation (manual import for now)
- Production deploy validation (covered by deploy spec)
