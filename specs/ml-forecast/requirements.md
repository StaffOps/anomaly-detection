# Feature: ML Forecast (Prophet Integration)

## User Stories

WHEN a metric series has ≥24h of historical data THEN the system SHALL periodically generate forecasts predicting future values and potential threshold breaches.

WHEN a forecast predicts a threshold breach within the forecast horizon THEN the system SHALL emit an anomaly with detector="ml_forecast" containing the predicted breach time.

WHEN an SRE receives a forecast-based alert THEN they SHALL see the predicted breach time and current trajectory, enabling proactive remediation before impact.

WHEN forecasts are generated THEN the system SHALL cache results in Redis to avoid redundant Prophet calls for the same series within the forecast window.

## Acceptance Criteria

- [ ] ForecastJob runs once per hour, selecting all series with ≥24h of baseline history
- [ ] ML client `Forecast` RPC is called with the historical sliding window for each eligible series
- [ ] Forecast results are cached in Redis with TTL equal to the forecast horizon (1h)
- [ ] Cached forecasts are wired into the correlator as detector="ml_forecast"
- [ ] Metrics exposed: `forecast_calls_total`, `forecast_duration_seconds`, `forecast_cache_hits`
- [ ] Series with <24h of data are skipped (no RPC call)
- [ ] Forecast accuracy is observable via comparison of predicted vs actual values (metric)

## Out of Scope

- Changing Prophet model parameters or hyperparameter tuning
- Model retraining or online learning
- Custom forecast horizons per series
- UI/dashboard for forecast visualization (use existing Grafana)
