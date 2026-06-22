# Design: ML Forecast (Prophet Integration)

## Architecture

```
┌─────────────┐    hourly     ┌──────────────┐   gRPC    ┌────────────┐
│ ForecastJob │───────────────▶│  ML Client   │──────────▶│ ML Service │
│ (goroutine) │               │  (Forecast)  │           │ (Prophet)  │
└──────┬──────┘               └──────────────┘           └────────────┘
       │                              │
       │ read history                 │ forecast result
       ▼                              ▼
┌──────────────┐              ┌──────────────┐
│ Baseline     │              │    Redis     │
│ Store        │              │  (cache)     │
│ (extended)   │              └──────┬───────┘
└──────────────┘                     │
                                     ▼
                              ┌──────────────┐
                              │  Correlator  │
                              │ detector=    │
                              │ "ml_forecast"│
                              └──────────────┘
```

## Components

| Component | Responsibility |
|-----------|---------------|
| Baseline Store (extended) | Store sliding window of raw `[]Point{Timestamp, Value}` alongside existing EWMA stats |
| ForecastJob | Hourly goroutine: enumerate eligible series, call ML, cache results |
| Redis forecast cache | Store forecast results with TTL=1h, keyed by series hash |
| Correlator integration | Read cached forecasts, emit anomalies with detector="ml_forecast" |

## Rationale

### Decision 1: Extend baseline store with sliding window (not separate store)

**Choice**: Add `[]Point` sliding window to existing baseline store entries alongside current stats (mean, stddev, EWMA).

**Justification**:
1. Single source of truth for per-series state — avoids consistency issues between two stores
2. Atomic updates — window and stats updated in same Redis pipeline call
3. Existing TTL/eviction logic applies uniformly

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Redis memory increase (~2x per series) | Acceptable: 288 points × 16 bytes = ~4.5KB/series. At 10K series = ~45MB |
| Larger Redis GET payloads | Only ForecastJob reads the window; normal detection reads only stats |

**When wrong**: If series count exceeds 100K and Redis memory becomes a bottleneck → move windows to a separate Redis DB or time-series store.

### Decision 2: Hourly forecast cadence

**Choice**: Run ForecastJob once per hour per eligible series.

**Justification**:
1. Prophet inference costs 500ms–2s per series — hourly bounds total compute budget
2. Forecasts for infrastructure metrics don't need sub-hour freshness
3. Matches TTL=1h for cache (no stale forecasts served)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Up to 1h stale prediction | Infrastructure metrics evolve slowly; acceptable |
| Burst of ML calls at tick | Bounded by series count; ML service handles concurrently |

**When wrong**: If rapid-onset anomalies require <10min prediction freshness → add event-triggered forecasts for high-priority series.

### Decision 3: History threshold of 24h (288 points at 5min intervals)

**Choice**: Skip series with fewer than 288 data points.

**Justification**:
1. Prophet needs sufficient seasonality signal — <24h produces unreliable forecasts
2. Avoids wasting ML compute on newly-discovered series
3. 24h captures at least one daily cycle

**When wrong**: If most series are short-lived (ephemeral pods) → lower to 6h or use simpler linear extrapolation fallback.

### Decision 4: Redis key schema for forecast cache

**Choice**: Key `forecast:{series_hash}`, value = serialized forecast proto, TTL = 1h.

**Justification**:
1. Same hash used by baseline store — no new hashing logic
2. TTL matches job cadence — cache always fresh or absent
3. Correlator does simple GET — no scan required

## Invariants

- ForecastJob MUST NOT block the detection loop (runs in separate goroutine)
- Forecast cache misses MUST NOT produce anomalies (only cache hits are wired)
- Baseline window MUST NOT grow unbounded (retention policy enforced on every write)

## Dependencies

| Service | Purpose |
|---------|---------|
| ML Service (gRPC :50051) | Prophet forecast inference |
| Redis | Baseline window storage + forecast cache |
| Prometheus-compatible TSDB | Source of raw metric values (scraped by workers) |
