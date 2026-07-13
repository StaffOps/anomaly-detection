"""Tests for the Prophet forecasting wrapper.

Prophet itself is mocked: fitting a real model is slow (~500ms-2s) and its
output is non-deterministic, which would make breach/confidence assertions
flaky. We assert the wrapper's contract instead — how it slices the horizon,
decides a breach, computes time-to-breach, and clamps confidence.
"""

from __future__ import annotations

import pandas as pd
import pytest

from server import forecaster as fc


class _FakeProphet:
    """Stand-in for prophet.Prophet: records the fit frame, returns a canned forecast."""

    last_fit_df: pd.DataFrame | None = None

    def __init__(self, *args, **kwargs):
        self.init_kwargs = kwargs
        self._forecast: pd.DataFrame | None = None

    def fit(self, df):
        _FakeProphet.last_fit_df = df

    def make_future_dataframe(self, periods, freq):
        return pd.DataFrame(
            {"ds": pd.date_range("2026-01-01", periods=periods, freq=freq)}
        )

    def predict(self, future):
        return self._forecast


@pytest.fixture
def patch_prophet(monkeypatch):
    """Install a Prophet factory that yields a fake pre-loaded with `forecast_df`."""

    def _install(forecast_df: pd.DataFrame):
        def factory(*args, **kwargs):
            model = _FakeProphet(*args, **kwargs)
            model._forecast = forecast_df
            return model

        monkeypatch.setattr(fc, "Prophet", factory)

    return _install


def _forecast_df(yhat, upper, lower) -> pd.DataFrame:
    return pd.DataFrame({"yhat": yhat, "yhat_upper": upper, "yhat_lower": lower})


def test_predict_breach_reports_time_to_breach(patch_prophet):
    # last 3 rows are the horizon; predicted crosses 100 at horizon index 1.
    patch_prophet(
        _forecast_df(
            yhat=[10, 20, 90, 110, 120],
            upper=[15, 25, 95, 130, 140],
            lower=[5, 15, 85, 90, 100],
        )
    )
    result = fc.Forecaster().predict(
        values=[90, 95, 100, 105, 110],
        timestamps=[1, 2, 3, 4, 5],
        horizon_minutes=3,
        breach_threshold=100.0,
    )

    assert result["predicted"] == [90, 110, 120]
    assert result["upper_bound"] == [95, 130, 140]
    assert result["lower_bound"] == [85, 90, 100]
    assert result["will_breach"] is True
    # first predicted point > threshold is horizon index 1 → 1/60 hours
    assert result["time_to_breach"] == pytest.approx(1 / 60.0)
    # confidence = 1 - mean(upper-lower)/mean(values) = 1 - 30/100
    assert result["confidence"] == pytest.approx(0.7, abs=1e-6)


def test_predict_no_breach_when_below_threshold(patch_prophet):
    patch_prophet(
        _forecast_df(
            yhat=[10, 20, 30, 40, 50],
            upper=[15, 25, 35, 45, 55],
            lower=[5, 15, 25, 35, 45],
        )
    )
    result = fc.Forecaster().predict(
        values=[10, 20, 30, 40, 50],
        timestamps=[1, 2, 3, 4, 5],
        horizon_minutes=2,
        breach_threshold=1000.0,
    )

    assert result["will_breach"] is False
    assert result["time_to_breach"] == 0.0


def test_confidence_clamped_to_zero_on_wide_interval(patch_prophet):
    # Interval far wider than the signal mean → raw confidence negative → clamp 0.
    patch_prophet(
        _forecast_df(
            yhat=[1, 1, 1],
            upper=[1000, 1000, 1000],
            lower=[0, 0, 0],
        )
    )
    result = fc.Forecaster().predict(
        values=[1, 1, 1],
        timestamps=[1, 2, 3],
        horizon_minutes=3,
        breach_threshold=10_000.0,
    )

    assert result["confidence"] == 0.0


def test_fit_receives_ds_and_y_columns(patch_prophet):
    patch_prophet(_forecast_df(yhat=[1, 2], upper=[2, 3], lower=[0, 1]))
    fc.Forecaster().predict(
        values=[5.0, 6.0],
        timestamps=[100, 160],
        horizon_minutes=2,
        breach_threshold=100.0,
    )

    fitted = _FakeProphet.last_fit_df
    assert list(fitted.columns) == ["ds", "y"]
    assert fitted["y"].tolist() == [5.0, 6.0]
    # timestamps interpreted as unix seconds
    assert str(fitted["ds"].iloc[0]) == "1970-01-01 00:01:40"
