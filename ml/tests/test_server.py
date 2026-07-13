"""Tests for the gRPC servicer and the serve() bootstrap.

The servicer is exercised in-process with a fake gRPC context and injected
forecaster/multivariate stubs — this keeps the tests fast and deterministic
while covering the real request/response marshalling against the proto
contract, plus the error paths (which must set INTERNAL + details and return
an empty response, never raise).
"""

from __future__ import annotations

import grpc
import pytest

from server import main
from server.generated import ml_pb2


class _FakeContext:
    """Minimal grpc.ServicerContext double capturing status + details."""

    def __init__(self):
        self.code = None
        self.details = None

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details


def _servicer(forecaster=None, multivariate=None) -> main.MLDetectorServicer:
    svc = main.MLDetectorServicer()
    if forecaster is not None:
        svc.forecaster = forecaster
    if multivariate is not None:
        svc.multivariate = multivariate
    return svc


# --------------------------------------------------------------------------- #
# Forecast
# --------------------------------------------------------------------------- #

def test_forecast_happy_path_maps_result_to_response():
    class _FC:
        def predict(self, values, timestamps, horizon_minutes, breach_threshold):
            return {
                "predicted": [1.0, 2.0],
                "upper_bound": [1.5, 2.5],
                "lower_bound": [0.5, 1.5],
                "will_breach": True,
                "time_to_breach": 0.25,
                "confidence": 0.8,
            }

    svc = _servicer(forecaster=_FC())
    req = ml_pb2.ForecastRequest(
        metric_name="cpu",
        values=[1.0, 2.0, 3.0],
        timestamps=[1, 2, 3],
        horizon_minutes=2,
        breach_threshold=10.0,
    )
    ctx = _FakeContext()

    resp = svc.Forecast(req, ctx)

    assert list(resp.predicted) == [1.0, 2.0]
    assert list(resp.upper_bound) == [1.5, 2.5]
    assert list(resp.lower_bound) == [0.5, 1.5]
    assert resp.will_breach_threshold is True
    assert resp.time_to_breach_hours == pytest.approx(0.25)
    assert resp.confidence == pytest.approx(0.8)
    assert ctx.code is None  # no error set


def test_forecast_error_sets_internal_and_returns_empty():
    class _Boom:
        def predict(self, values, timestamps, horizon_minutes, breach_threshold):
            raise ValueError("prophet exploded")

    svc = _servicer(forecaster=_Boom())
    ctx = _FakeContext()

    resp = svc.Forecast(ml_pb2.ForecastRequest(values=[1.0]), ctx)

    assert ctx.code == grpc.StatusCode.INTERNAL
    assert "prophet exploded" in ctx.details
    assert list(resp.predicted) == []  # empty response, not an exception


# --------------------------------------------------------------------------- #
# DetectMultivariate
# --------------------------------------------------------------------------- #

def test_detect_multivariate_happy_path():
    class _MV:
        def detect(self, samples):
            assert samples == {"cpu_ratio": 0.9, "error_rate_1m": 0.2}
            return {"is_anomaly": True, "score": 0.77, "contributors": ["cpu_ratio"]}

    svc = _servicer(multivariate=_MV())
    req = ml_pb2.MultivariateRequest(
        samples=[
            ml_pb2.MetricSample(name="cpu_ratio", value=0.9),
            ml_pb2.MetricSample(name="error_rate_1m", value=0.2),
        ]
    )
    ctx = _FakeContext()

    resp = svc.DetectMultivariate(req, ctx)

    assert resp.is_anomaly is True
    assert resp.anomaly_score == pytest.approx(0.77)
    assert list(resp.contributing_metrics) == ["cpu_ratio"]
    assert ctx.code is None


def test_detect_multivariate_error_sets_internal_and_returns_empty():
    class _Boom:
        def detect(self, samples):
            raise RuntimeError("forest exploded")

    svc = _servicer(multivariate=_Boom())
    ctx = _FakeContext()

    resp = svc.DetectMultivariate(
        ml_pb2.MultivariateRequest(samples=[ml_pb2.MetricSample(name="x", value=1.0)]),
        ctx,
    )

    assert ctx.code == grpc.StatusCode.INTERNAL
    assert "forest exploded" in ctx.details
    assert resp.is_anomaly is False


# --------------------------------------------------------------------------- #
# Health
# --------------------------------------------------------------------------- #

def test_health_reports_ready_and_model_version():
    resp = _servicer().Health(ml_pb2.Empty(), _FakeContext())
    assert resp.ready is True
    assert resp.model_version == main.MODEL_VERSION


# --------------------------------------------------------------------------- #
# serve() bootstrap
# --------------------------------------------------------------------------- #

def test_serve_wires_server_metrics_and_gauges(monkeypatch):
    calls = {}

    class _FakeServer:
        def __init__(self):
            self.terminated = False

        def add_insecure_port(self, addr):
            calls["addr"] = addr

        def start(self):
            calls["started"] = True

        def wait_for_termination(self):
            calls["waited"] = True  # returns immediately in test

    fake_server = _FakeServer()

    monkeypatch.setattr(main, "start_http_server", lambda port: calls.setdefault("metrics_port", port))
    monkeypatch.setattr(main.grpc, "server", lambda executor: fake_server)
    monkeypatch.setattr(
        main.ml_pb2_grpc,
        "add_MLDetectorServicer_to_server",
        lambda servicer, server: calls.setdefault("registered", True),
    )

    main.serve(port=51000, metrics_port=8099)

    assert calls["metrics_port"] == 8099
    assert calls["addr"] == "[::]:51000"
    assert calls["started"] is True
    assert calls["registered"] is True
    assert calls["waited"] is True
    # readiness gauge flipped on
    assert main.ML_READY._value.get() == 1
