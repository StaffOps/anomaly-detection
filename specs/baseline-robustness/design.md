# Design: Baseline Robustness

## Architecture

All three sub-items modify `internal/baseline/store.go` (and its Redis interaction layer). No new services; changes are internal to the controller process.

```
┌─────────────────────────────────────────────────┐
│  internal/baseline/store.go                     │
│                                                 │
│  Sample in ──► ExtractWorkload() ──► Hash key   │  ← P2.8
│              │                                  │
│              ├─► PoisonGate() ──► accept/reject │  ← P2.9
│              │                                  │
│              └─► TrackEmission() ──► last_seen  │  ← P2.10
│                                                 │
│  Background:  AbsenceChecker (ticker)           │  ← P2.10
└─────────────────────────────────────────────────┘
```

## Components

| Component | Responsibility |
|-----------|----------------|
| `ExtractWorkload()` | Normalize labels → stable workload identity |
| `PoisonGate` | Decide accept/reject before EWMA update |
| `EmissionTracker` | Track `last_seen` + expected interval per series |
| `AbsenceChecker` | Periodic scan for silent series → emit alert |

## Rationale

### Decision 1: Workload-identity keying via label normalization (P2.8)

**Choice**: Strip pod-name, pod-uid, pod-template-hash, and replica-index from label set before hashing the baseline key. Keep: namespace, workload-name (deployment/sts/daemonset), container, metric-name.

**Justification**:
1. Rolling updates create new pod names every time — current pod-keyed baselines reset on every deploy, causing false positives for 15–30 min post-deploy
2. Workload is the stable identity that SREs reason about
3. Minimal code change (one normalization function before the existing hash call)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Loses per-replica granularity | Acceptable — optional two-tier (workload + replica deviation) covers edge cases |
| Old Redis keys become orphans | TTL expiry handles cleanup within 24h |

**When this decision would be wrong**:
- If per-pod baselines are needed for canary-specific detection (would require explicit opt-in keying)

### Decision 2: Poisoning gate with regime-change escape (P2.9)

**Choice**: Before updating EWMA, check `|sample - mean| > poisoning_threshold * stddev`. If true, increment rejection counter; skip update. After `regime_change_samples` consecutive rejections, accept new level as regime change and reset baseline.

**Justification**:
1. Prevents slow-ramp attacks from shifting the baseline — the primary threat from the review
2. Regime-change escape prevents permanent lockout when legitimate traffic patterns shift (deploy of new feature, traffic growth)
3. Threshold (default 5σ) is conservative — normal fluctuation won't trigger

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Legitimate sudden spikes rejected for N samples | 30 samples ≈ 5 min at 10s interval — acceptable detection delay for true regime change |
| Adds state (rejection counter per series) | Single int in Redis, negligible |

**When this decision would be wrong**:
- If workloads have frequent legitimate step-changes (would need lower `regime_change_samples` or explicit "baseline reset" API)

### Decision 3: Emission tracking with suppression list (P2.10)

**Choice**: Maintain `last_seen` timestamp per series in Redis. Background goroutine scans every 30s. Alert when `now - last_seen > absence_threshold`. Suppress series matching namespace/label patterns in config (KEDA, crons).

**Justification**:
1. Dead services produce zero signal — existing detection only fires on anomalous VALUES, not absence
2. Suppression list avoids false alerts on intentionally-idle workloads (batch jobs, KEDA scale-to-zero)
3. Redis-native (HSET with TTL) — no new infrastructure

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Extra Redis reads on scan tick | One HSCAN per tick (30s), bounded by active series count (~thousands) |
| Startup grace period masks real outages during controller restart | 2× absence_threshold grace — controller restarts are <30s typically |

**When this decision would be wrong**:
- If series count grows to 100k+ (HSCAN latency) — would need sharded tracking or bloom filter

## Invariants

- Baseline key MUST be deterministic for the same workload across pod restarts
- PoisonGate MUST NOT block the sample from being used in detection (only blocks baseline UPDATE)
- AbsenceChecker MUST NOT fire during first `startup_grace` period after controller start
- Suppression config changes MUST take effect without controller restart (watch config reload)

## Dependencies

| Service | Purpose |
|---------|---------|
| Redis | Baseline storage, emission tracking, rejection counters |
| Alertmanager | Absence alerts delivery |
| Config (YAML) | Thresholds, suppression patterns |
