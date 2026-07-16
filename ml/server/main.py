"""ML Detector gRPC server — Prophet forecasting + Isolation Forest."""

import logging
import time
from concurrent import futures

import grpc
from otel_helper import TelemetryOptions, get_meter, setup_telemetry

from server.generated import ml_pb2, ml_pb2_grpc
from server.forecaster import Forecaster
from server.multivariate import MultivariateDetector

logger = logging.getLogger(__name__)

# =============================================================================
# Metric naming convention: staffops_ad_ml_<metric>
#
# Instruments are created via the OTel Metrics API (otel_helper.get_meter),
# exported through the library's Prometheus /metrics reader — no direct
# prometheus_client usage. get_meter() proxies the global MeterProvider (same
# delegating pattern as get_tracer), so these are safe to record on before
# setup_telemetry() runs in serve().
# =============================================================================

meter = get_meter("staffops-ad-ml")

# Request-level metrics (per RPC method)
ML_REQUESTS = meter.create_counter(
    "staffops_ad_ml_requests_total",
    description="Total gRPC requests received by the ML service",
)
ML_REQUEST_DURATION = meter.create_histogram(
    "staffops_ad_ml_request_duration_seconds",
    description="Duration of ML gRPC requests",
    explicit_bucket_boundaries_advisory=(0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0),
)

# Forecast-specific
ML_FORECAST_BREACH_PREDICTED = meter.create_counter(
    "staffops_ad_ml_forecast_breach_predicted_total",
    description="Number of forecasts that predicted a threshold breach",
)
ML_FORECAST_INPUT_SIZE = meter.create_histogram(
    "staffops_ad_ml_forecast_input_size",
    description="Number of historical points received per forecast request",
    explicit_bucket_boundaries_advisory=(10, 30, 60, 120, 240, 480, 1440),
)
ML_FORECAST_CONFIDENCE = meter.create_histogram(
    "staffops_ad_ml_forecast_confidence",
    description="Confidence score of forecasts (0..1)",
    explicit_bucket_boundaries_advisory=(0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99),
)

# Multivariate-specific
ML_MULTIVARIATE_ANOMALIES = meter.create_counter(
    "staffops_ad_ml_multivariate_anomalies_total",
    description="Number of multivariate anomalies detected by Isolation Forest",
)
ML_MULTIVARIATE_INPUT_SIZE = meter.create_histogram(
    "staffops_ad_ml_multivariate_input_size",
    description="Number of metric samples per multivariate request",
    explicit_bucket_boundaries_advisory=(2, 5, 10, 25, 50, 100),
)
ML_MULTIVARIATE_SCORE = meter.create_histogram(
    "staffops_ad_ml_multivariate_score",
    description="Anomaly score returned by Isolation Forest (0..1)",
    explicit_bucket_boundaries_advisory=(0.1, 0.25, 0.5, 0.7, 0.8, 0.9, 0.95),
)

# Service health
ML_READY = meter.create_gauge(
    "staffops_ad_ml_ready",
    description="1 if ML service is ready to serve requests",
)
ML_MODEL_VERSION = meter.create_gauge(
    "staffops_ad_ml_model_version_info",
    description="Model version metadata (always 1)",
)

MODEL_VERSION = "0.2.0"


class MLDetectorServicer(ml_pb2_grpc.MLDetectorServicer):
    def __init__(self):
        self.forecaster = Forecaster()
        self.multivariate = MultivariateDetector()

    def Forecast(self, request, context):
        start = time.perf_counter()
        try:
            ML_FORECAST_INPUT_SIZE.record(len(request.values))
            result = self.forecaster.predict(
                values=list(request.values),
                timestamps=list(request.timestamps),
                horizon_minutes=request.horizon_minutes,
                breach_threshold=request.breach_threshold,
            )
            ML_FORECAST_CONFIDENCE.record(result["confidence"])
            if result["will_breach"]:
                ML_FORECAST_BREACH_PREDICTED.add(1)
            ML_REQUESTS.add(1, {"method": "Forecast", "status": "ok"})
            return ml_pb2.ForecastResponse(
                predicted=result["predicted"],
                upper_bound=result["upper_bound"],
                lower_bound=result["lower_bound"],
                will_breach_threshold=result["will_breach"],
                time_to_breach_hours=result["time_to_breach"],
                confidence=result["confidence"],
            )
        except Exception as e:
            ML_REQUESTS.add(1, {"method": "Forecast", "status": "error"})
            logger.exception("forecast failed: %s", e)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return ml_pb2.ForecastResponse()
        finally:
            ML_REQUEST_DURATION.record(time.perf_counter() - start, {"method": "Forecast"})

    def DetectMultivariate(self, request, context):
        start = time.perf_counter()
        try:
            ML_MULTIVARIATE_INPUT_SIZE.record(len(request.samples))
            samples = {s.name: s.value for s in request.samples}
            result = self.multivariate.detect(samples)
            ML_MULTIVARIATE_SCORE.record(result["score"])
            if result["is_anomaly"]:
                ML_MULTIVARIATE_ANOMALIES.add(1)
            ML_REQUESTS.add(1, {"method": "DetectMultivariate", "status": "ok"})
            return ml_pb2.MultivariateResponse(
                is_anomaly=result["is_anomaly"],
                anomaly_score=result["score"],
                contributing_metrics=result["contributors"],
            )
        except Exception as e:
            ML_REQUESTS.add(1, {"method": "DetectMultivariate", "status": "error"})
            logger.exception("multivariate failed: %s", e)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return ml_pb2.MultivariateResponse()
        finally:
            ML_REQUEST_DURATION.record(time.perf_counter() - start, {"method": "DetectMultivariate"})

    def Health(self, request, context):
        ML_REQUESTS.add(1, {"method": "Health", "status": "ok"})
        return ml_pb2.HealthResponse(ready=True, model_version=MODEL_VERSION)


def serve(port: int = 50051, metrics_port: int = 8082):
    setup_telemetry(TelemetryOptions(
        service_name="staffops-ad-ml",
        metric_exporters=["prometheus"],
        prometheus_metrics_port=metrics_port,
    ))
    logger.info(f"Prometheus metrics on :{metrics_port}")

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    ml_pb2_grpc.add_MLDetectorServicer_to_server(MLDetectorServicer(), server)
    server.add_insecure_port(f"[::]:{port}")
    server.start()

    ML_READY.set(1)
    ML_MODEL_VERSION.set(1, {"version": MODEL_VERSION})
    logger.info(f"ML Detector gRPC server on :{port}")

    server.wait_for_termination()


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    serve()
