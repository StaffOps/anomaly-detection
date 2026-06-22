# Feature: Baseline Robustness

Three gaps identified in threat-model review (2026-06-14), all grounded in `internal/baseline/store.go`.

## User Stories

### P2.8 — Workload-identity baseline keying

AS an SRE,
WHEN pods restart during a rolling update
THEN baselines SHALL survive because they are keyed by workload identity (deployment/statefulset), not ephemeral pod name.

### P2.9 — Outlier rejection (anti-poisoning)

AS a security engineer,
WHEN an attacker slowly ramps malicious load over time
THEN the baseline SHALL NOT absorb the ramp — poisoning protection gates updates when samples are anomalous.

### P2.10 — Absence-of-signal detection (dead man's switch)

AS an on-call engineer,
WHEN a previously-active service goes completely silent (zero emissions)
THEN I SHALL be alerted within a configurable threshold, so I can investigate before users notice.

## Acceptance Criteria

### P2.8
- [ ] Baseline key derived from workload labels (namespace, deployment/statefulset name, container) — NOT pod name, pod UID, or replica hash
- [ ] Rolling update does not reset or fork baselines
- [ ] Existing baselines in Redis migrate transparently (old keys expire, new keys populated on next sample)
- [ ] Per-replica deviation optionally tracked as second tier without polluting workload-level baseline

### P2.9
- [ ] Baseline update skipped when sample exceeds `poisoning_threshold` × current stddev (configurable, default 5σ)
- [ ] After N consecutive sustained samples above threshold (configurable, default 30), regime-change logic accepts the new level
- [ ] Poisoning rejection events logged with series key + sample value + threshold at time of rejection
- [ ] Unit test demonstrates that a linear 10-minute ramp does NOT shift the baseline mean by >10%

### P2.10
- [ ] Each active series tracked with `last_seen` timestamp and expected emission interval
- [ ] Alert fires when `now - last_seen > absence_threshold` (configurable, default 5× expected interval)
- [ ] Suppression for known scale-to-zero workloads (KEDA ScaledObjects, CronJobs) via namespace/label match in config
- [ ] Alert clears automatically when emissions resume
- [ ] No false positives during controller restart (grace period on startup)

## Out of Scope

- Changing detection algorithms (Z-Score, EWMA remain as-is)
- ML integration (Prophet/Isolation Forest pipeline unchanged)
- Alertmanager routing changes
- Redis schema version migration tooling (handled by TTL expiry)
