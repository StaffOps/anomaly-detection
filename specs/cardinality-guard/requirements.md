# Feature: Cardinality Guard

## User Story

AS an operator running anomaly-detection workers,
WHEN label cardinality explodes upstream (misconfigured exporter, new high-cardinality label),
THEN the system SHALL protect itself from Redis OOM by refusing to create new baselines above a configurable threshold.

## Acceptance Criteria

- [ ] Threshold is configurable via `max_baseline_series` config field (default: 10000)
- [ ] Workers expose metric `staffops_ad_worker_baseline_series_tracked` (gauge, current count)
- [ ] When series count ≥ threshold, new baseline creation is rejected (skip, not error)
- [ ] Existing baselines continue to be updated normally above threshold
- [ ] Alert annotation fires when threshold is breached (via existing alerting path)
- [ ] Worker logs a warning on first rejection per evaluation cycle

## Out of Scope

- Fixing the upstream source of cardinality explosion (operational concern)
- Automatic cleanup/eviction of existing baselines
- Per-namespace or per-metric granularity (single global threshold is sufficient for P5.4)
