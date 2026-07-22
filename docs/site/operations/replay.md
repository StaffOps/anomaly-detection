# Replay Mode

## Overview

Simulate detection over historical metrics/logs with a candidate config, **before** applying it in production. Zero side effects.

```mermaid
graph LR
    A[Historical data] --> B[Replay Engine]
    B --> C[In-memory baselines]
    C --> D[Detection]
    D --> E[Report JSON + MD]
```

**Guarantees:**

- ❌ No Redis writes
- ❌ No Alertmanager dispatches
- ❌ No gRPC fan-out to workers
- ❌ No ML calls (V1)
- ✅ Uses same detection engine as production

## Usage

```bash
controller --replay \
  --from=24h \
  --to=now \
  --config=candidate.yaml \
  --output=report.json
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--replay` | `false` | Enable replay mode |
| `--from` | (required) | Window start — duration (`24h`, `30m`, `7d`) or RFC3339 timestamp |
| `--to` | `now` | Window end — duration or RFC3339 |
| `--config` | `config.yaml` | Config file to evaluate |
| `--output` | `./replay-report.json` | Report output path (`.json` + `.md` written) |
| `--warmup-fraction` | `0.2` | Fraction of window used to warm baselines |
| `--max-range` | `7d` | Maximum allowed window size |
| `--max-anomalies` | `1000` | Cap anomalies in report |
| `--inject` | `` | Path to a synthetic-fault injection profile (empty/`none` = off) |

!!! note "Detection parity with production"
    The in-memory baseline mirrors the production baseline **exactly**, including
    detecting the z-score against the *pre-update* stats and the stddev floor. (It
    did not before 2026-07-19 — it detected on post-update stats and under-fired;
    replay reports from before that fix under-count anomalies.)

### Synthetic fault injection (`--inject`)

Pass an injection profile to perturb series in-memory (spike/ramp/step/silence) over
a window and score detection against the injected ground truth. The report then carries
an **Injection Scoring** block (precision/recall/F1, TP/FP/FN, recall-by-type) in both
the JSON and the markdown. This is the basis of the P0.1 recall/FP measurement — see
`specs/synthetic-injection/`. Faults scale by the series' own stddev, so target
series that actually vary (a flat series can't be perturbed meaningfully).

## How It Works

### 1. Window Parsing

Accepts relative durations or absolute timestamps:

```bash
--from=24h --to=now          # Last 24 hours
--from=24h --to=1h           # 24h ago to 1h ago
--from=2026-05-30T00:00:00Z  # Absolute start
```

### 2. Warm-up Phase

The first portion of the window is used to build baselines (no anomalies emitted):

```
warmup_duration = max(0.2 × window, warm_up_samples × tick_interval)
```

For a 24h window with defaults: `max(4.8h, 30min) = 4.8h` warm-up.

### 3. Tick Simulation

After warm-up, the engine simulates detection cycles:

- Fetches data in 1-hour chunks via Prometheus/Loki range queries
- Iterates ticks at `job_interval` (30s) steps
- Runs the same detection engine as production
- Accumulates anomalies in memory

#### Query failures degrade per rule, not per tick

If one rule's query fails, only that rule is skipped for that tick — the others still
evaluate, and the failure is counted in `totals.query_errors`. A tick counts as
`ticks_skipped_query_error` only when **every** adaptive query failed, since nothing
could be evaluated.

This mirrors production: one rule's query failing does not blind the rest, and the BH
family is whatever actually got evaluated.

!!! warning "Read `query_errors` before trusting a low anomaly count"
    A run can complete successfully and still report near-zero anomalies simply because
    an expensive rule failed on every tick. The two look identical in the totals. Always
    check `query_errors` and `ticks_skipped_query_error` before concluding that a rule
    set is quiet.

    Common cause: a histogram rule over a very large series family exceeding the
    backend's limits — on VictoriaMetrics, `cannot select more than
    -search.maxSamplesPerQuery`. Narrow the rule's label filters, or raise the limit on
    the query tier used for replay.

!!! tip "Run replay close to the data"
    Replay issues one range query per rule per tick, so a 6h window at a 60s interval
    with ~24 rules is ~8600 range queries. Measured over a `kubectl port-forward` from a
    laptop this ran at ~100s per tick (≈10h for the window); the same work in-cluster
    runs in tens of minutes. For anything beyond a smoke test, run replay as a Job in
    the cluster.

### 4. Report Generation

Outputs both JSON and Markdown:

- `report.json` — machine-readable, full anomaly details
- `report.md` — human-readable tables with ASCII sparklines

## Report Structure

```json
{
  "metadata": {
    "schema_version": "1",
    "window": {"from": "...", "to": "..."},
    "warmup_end": "...",
    "config_path": "candidate.yaml",
    "result_status": "anomalies_detected",
    "execution_metrics": {
      "duration_seconds": 45.2,
      "ticks_processed": 2304,
      "ticks_skipped_query_error": 3,
      "vm_queries_total": 4608,
      "memory_peak_mb": 128
    }
  },
  "totals": {
    "anomalies": 142,
    "by_severity": {"warning": 98, "critical": 44},
    "by_detector": {"static": 23, "adaptive": 119},
    "by_kind": {"pod": 130, "workload": 12},
    "top_workloads": [...]
  },
  "timeline": [...],
  "anomalies": [...]
}
```

## Use Cases

### Tune Z-Score Threshold

```bash
# Current threshold (3.0)
controller --replay --from=24h --config=config.yaml --output=baseline.json

# Candidate threshold (3.5 — less sensitive)
controller --replay --from=24h --config=candidate.yaml --output=candidate.json

# Compare: candidate should have fewer anomalies
jq '.totals.anomalies' baseline.json candidate.json
```

### Validate New Rules

```bash
# Add a new adaptive metric, replay to see what it catches
controller --replay --from=7d --config=new-rules.yaml --output=new-rules.json
```

### Identify Noisy Workloads

```bash
# Check top workloads by anomaly count
jq '.totals.top_workloads[:10]' report.json
```

## Constraints

| Constraint | Value | Reason |
|------------|-------|--------|
| Minimum window | 2.5h | Warm-up needs enough data |
| Maximum window | 7d (configurable) | Memory and query volume |
| Max anomalies | 1000 (configurable) | Report size |
| ML | Disabled in V1 | Stateful model doesn't fit replay |

## Error Handling

- **Query failure on a tick**: logged as warning, tick skipped, replay continues
- **SIGTERM/SIGINT**: flushes partial report (marked `partial`), exits cleanly
- **Pre-flight failure** (Prometheus/Loki unreachable): fails fast before processing

!!! info "Status"
    Replay mode is 75% complete (12/16 tasks). Integration test, smoke test, and final docs are pending. See [Roadmap](../roadmap.md).
