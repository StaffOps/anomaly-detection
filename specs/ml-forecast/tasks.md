# Tasks: ML Forecast (Prophet Integration)

> **Status**: `FUTURE` — Blocked on baseline store maturity + Phase 0 outcome

- [ ] T1: Extend baseline store schema — add sliding window `[]Point{Timestamp, Value}` alongside existing stats
- [ ] T2: Implement window retention policy — configurable max points, default 288 (24h at 5min intervals), enforced on every append
- [ ] T3: Implement ForecastJob goroutine — hourly ticker, enumerates series from baseline store, selects those with ≥288 points
- [ ] T4: Call ML client `Forecast` RPC with historical window for each eligible series
- [ ] T5: Cache forecast result in Redis — key: `forecast:{series_hash}`, TTL: 1h
- [ ] T6: Wire cached forecasts into correlator as detector="ml_forecast" — read on each correlation cycle, emit anomaly if breach predicted
- [ ] T7: Add Prometheus metrics — `forecast_calls_total`, `forecast_duration_seconds`, `forecast_cache_hits`
- [ ] T8: Tests for ForecastJob — mock ML client, verify hourly scheduling, caching, history threshold filtering
