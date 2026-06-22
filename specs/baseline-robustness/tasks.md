# Tasks: Baseline Robustness

> **Status**: `TODO` — Ready to implement, no external blockers. Touches internal/baseline/store.go.

## P2.8 — Workload-identity baseline keying

- [ ] Task 1: Implement `ExtractWorkload(labels map[string]string) map[string]string` — strips pod, pod-uid, pod-template-hash, replica-index; keeps namespace, workload-name, container, metric
- [ ] Task 2: Replace current key-hash input in `store.go` with `ExtractWorkload()` output
- [ ] Task 3: Add config option `baseline.two_tier_enabled` (default false) for optional per-replica deviation tracking
- [ ] Task 4: Unit tests — verify same workload across pod names produces identical key; verify two-tier stores both levels

## P2.9 — Outlier rejection (anti-poisoning)

- [ ] Task 5: Implement `PoisonGate` struct with `ShouldReject(sample, mean, stddev float64) bool` using configurable `poisoning_threshold` (default 5.0)
- [ ] Task 6: Add rejection counter per series in Redis (HINCRBY); reset on accept
- [ ] Task 7: Implement regime-change logic — accept after `regime_change_samples` (default 30) consecutive rejections; reset baseline to recent window
- [ ] Task 8: Integrate PoisonGate into `store.Update()` — gate EWMA update, pass sample through to detection unchanged
- [ ] Task 9: Unit tests — linear ramp over 10min must NOT shift mean >10%; regime change after N samples must converge

## P2.10 — Absence-of-signal detection (dead man's switch)

- [ ] Task 10: Implement `EmissionTracker` — HSET `last_seen` on every sample; compute expected interval from observed cadence
- [ ] Task 11: Implement `AbsenceChecker` background goroutine — HSCAN every 30s, fire alert when `now - last_seen > absence_threshold` (default 5× interval)
- [ ] Task 12: Add suppression config (`absence.suppress_patterns[]` with namespace/label matchers); add startup grace period (skip alerts for `2× absence_threshold` after boot)
- [ ] Task 13: Unit tests — verify alert fires on silence; verify suppression prevents alert; verify grace period prevents startup false positives
