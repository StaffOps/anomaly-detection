"""Tests for the Isolation Forest multivariate wrapper.

The sklearn model is replaced with a controllable fake so the anomaly
decision and score are deterministic. We assert the wrapper's contract:
canonical feature padding, the warm-up threshold, periodic refitting, and
the contributor-selection logic (top-3 features with |z| > 2).
"""

from __future__ import annotations

import numpy as np

from server import multivariate as mv
from server.multivariate import CANONICAL_FEATURES, MultivariateDetector


class _FakeForest:
    """Deterministic stand-in for sklearn IsolationForest.

    `verdict` controls predict() (-1 anomaly / 1 normal); `score` controls
    score_samples() (negated by the wrapper, so higher score → more anomalous).
    """

    def __init__(self, verdict: int = 1, score: float = -0.3):
        self.verdict = verdict
        self.score = score
        self.fit_calls = 0

    def fit(self, X):
        self.fit_calls += 1

    def predict(self, X):
        return np.array([self.verdict])

    def score_samples(self, X):
        return np.array([self.score])


def _detector_with(model: _FakeForest, min_samples: int = 50) -> MultivariateDetector:
    det = MultivariateDetector()
    det.model = model
    det._min_samples = min_samples
    return det


def test_normalize_pads_missing_and_drops_unknown():
    det = MultivariateDetector()
    out = det._normalize({"cpu_ratio": 0.9, "not_a_feature": 123.0})

    assert set(out.keys()) == set(CANONICAL_FEATURES)
    assert out["cpu_ratio"] == 0.9
    assert out["memory_ratio"] == 0.0  # padded
    assert "not_a_feature" not in out


def test_below_min_samples_returns_safe_default():
    det = _detector_with(_FakeForest(), min_samples=5)
    result = det.detect({"cpu_ratio": 0.5})

    assert result == {"is_anomaly": False, "score": 0.0, "contributors": []}
    # not enough history yet → model never fitted
    assert det.model.fit_calls == 0


def test_fits_once_warm_then_scores_normal_sample():
    model = _FakeForest(verdict=1, score=-0.2)
    det = _detector_with(model, min_samples=3)

    det.detect({"cpu_ratio": 0.1})
    det.detect({"cpu_ratio": 0.1})
    result = det.detect({"cpu_ratio": 0.1})  # 3rd → warm

    assert model.fit_calls == 1  # fitted exactly once on first warm cycle
    assert not result["is_anomaly"]
    assert result["score"] == 0.2  # -score_samples
    assert result["contributors"] == []


def test_anomaly_selects_high_zscore_contributors():
    model = _FakeForest(verdict=-1, score=-0.9)
    det = _detector_with(model, min_samples=3)

    # Two calm baseline samples, then a spike on cpu_ratio + error_rate_1m.
    det.detect({"cpu_ratio": 0.10, "error_rate_1m": 0.01})
    det.detect({"cpu_ratio": 0.11, "error_rate_1m": 0.01})
    result = det.detect({"cpu_ratio": 5.0, "error_rate_1m": 3.0})

    assert result["is_anomaly"]
    assert result["score"] == 0.9
    # spiked features must surface; padded-constant features (z=0) must not.
    assert "cpu_ratio" in result["contributors"]
    assert "error_rate_1m" in result["contributors"]
    assert len(result["contributors"]) <= 3
    assert "memory_ratio" not in result["contributors"]


def test_refits_every_100_samples(monkeypatch):
    model = _FakeForest(verdict=1, score=-0.1)
    det = _detector_with(model, min_samples=1)

    # Pre-load 99 samples of history without triggering the periodic refit path
    # beyond the initial fit.
    for _ in range(99):
        det.detect({"cpu_ratio": 0.1})
    fits_after_99 = model.fit_calls

    # 100th sample → len(history) % 100 == 0 → refit.
    det.detect({"cpu_ratio": 0.1})
    assert model.fit_calls == fits_after_99 + 1


def test_default_contamination_constructs_real_model():
    # Guard the real constructor path (no injection) so import + wiring is covered.
    det = MultivariateDetector(contamination=0.1)
    assert det._min_samples == 50
    assert det._fitted is False
    assert isinstance(det.model, mv.IsolationForest)
