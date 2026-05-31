"""ML Detector gRPC server — Prophet forecasting + Isolation Forest."""

import logging
from concurrent import futures

import grpc
from prometheus_client import start_http_server, Counter, Histogram, Gauge

from server.generated import ml_pb2, ml_pb2_grpc
from server.forecaster import Forecaster
from server.multivariate import MultivariateDetector

logger = logging.getLogger(__name__)

# =============================================================================
# Metric naming convention: staffops_ad_ml_<metric>
# =============================================================================

# Request-level metrics (per RPC method)
ML_REQUESTS = Counter(
    "staffops_ad_ml_requests_total",
    "Total gRPC requests received by the ML service",
    ["method", "status"],
)
ML_REQUEST_DURATION = Histogram(
    "staffops_ad_ml_request_duration_seconds",
    "Duration of ML gRPC requests",
    ["method"],
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0),
)

# Forecast-specific
ML_FORECAST_BREACH_PREDICTED = Counter(
    "staffops_ad_ml_forecast_breach_predicted_total",
    "Number of forecasts that predicted a threshold breach",
)
ML_FORECAST_INPUT_SIZE = Histogram(
    "staffops_ad_ml_forecast_input_size",
    "Number of historical points received per forecast request",
    buckets=(10, 30, 60, 120, 240, 480, 1440),
)
ML_FORECAST_CONFIDENCE = Histogram(
    "staffops_ad_ml_forecast_confidence",
    "Confidence score of forecasts (0..1)",
    buckets=(0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99),
)

# Multivariate-specific
ML_MULTIVARIATE_ANOMALIES = Counter(
    "staffops_ad_ml_multivariate_anomalies_total",
    "Number of multivariate anomalies detected by Isolation Forest",
)
ML_MULTIVARIATE_INPUT_SIZE = Histogram(
    "staffops_ad_ml_multivariate_input_size",
    "Number of metric samples per multivariate request",
    buckets=(2, 5, 10, 25, 50, 100),
)
ML_MULTIVARIATE_SCORE = Histogram(
    "staffops_ad_ml_multivariate_score",
    "Anomaly score returned by Isolation Forest (0..1)",
    buckets=(0.1, 0.25, 0.5, 0.7, 0.8, 0.9, 0.95),
)

# Service health
ML_READY = Gauge(
    "staffops_ad_ml_ready",
    "1 if ML service is ready to serve requests",
)
ML_MODEL_VERSION = Gauge(
    "staffops_ad_ml_model_version_info",
    "Model version metadata (always 1)",
    ["version"],
)

MODEL_VERSION = "0.2.0"


class MLDetectorServicer(ml_pb2_grpc.MLDetectorServicer):
    def __init__(self):
        self.forecaster = Forecaster()
        self.multivariate = MultivariateDetector()

    def Forecast(self, request, context):
        with ML_REQUEST_DURATION.labels(method="Forecast").time():
            try:
                ML_FORECAST_INPUT_SIZE.observe(len(request.values))
                result = self.forecaster.predict(
                    values=list(request.values),
                    timestamps=list(request.timestamps),
                    horizon_minutes=request.horizon_minutes,
                    breach_threshold=request.breach_threshold,
                )
                ML_FORECAST_CONFIDENCE.observe(result["confidence"])
                if result["will_breach"]:
                    ML_FORECAST_BREACH_PREDICTED.inc()
                ML_REQUESTS.labels(method="Forecast", status="ok").inc()
                return ml_pb2.ForecastResponse(
                    predicted=result["predicted"],
                    upper_bound=result["upper_bound"],
                    lower_bound=result["lower_bound"],
                    will_breach_threshold=result["will_breach"],
                    time_to_breach_hours=result["time_to_breach"],
                    confidence=result["confidence"],
                )
            except Exception as e:
                ML_REQUESTS.labels(method="Forecast", status="error").inc()
                logger.exception("forecast failed: %s", e)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(str(e))
                return ml_pb2.ForecastResponse()

    def DetectMultivariate(self, request, context):
        with ML_REQUEST_DURATION.labels(method="DetectMultivariate").time():
            try:
                ML_MULTIVARIATE_INPUT_SIZE.observe(len(request.samples))
                samples = {s.name: s.value for s in request.samples}
                result = self.multivariate.detect(samples)
                ML_MULTIVARIATE_SCORE.observe(result["score"])
                if result["is_anomaly"]:
                    ML_MULTIVARIATE_ANOMALIES.inc()
                ML_REQUESTS.labels(method="DetectMultivariate", status="ok").inc()
                return ml_pb2.MultivariateResponse(
                    is_anomaly=result["is_anomaly"],
                    anomaly_score=result["score"],
                    contributing_metrics=result["contributors"],
                )
            except Exception as e:
                ML_REQUESTS.labels(method="DetectMultivariate", status="error").inc()
                logger.exception("multivariate failed: %s", e)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(str(e))
                return ml_pb2.MultivariateResponse()

    def Health(self, request, context):
        ML_REQUESTS.labels(method="Health", status="ok").inc()
        return ml_pb2.HealthResponse(ready=True, model_version=MODEL_VERSION)


def serve(port: int = 50051, metrics_port: int = 8082):
    start_http_server(metrics_port)
    logger.info(f"Prometheus metrics on :{metrics_port}")

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    ml_pb2_grpc.add_MLDetectorServicer_to_server(MLDetectorServicer(), server)
    server.add_insecure_port(f"[::]:{port}")
    server.start()

    ML_READY.set(1)
    ML_MODEL_VERSION.labels(version=MODEL_VERSION).set(1)
    logger.info(f"ML Detector gRPC server on :{port}")

    server.wait_for_termination()


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    serve()
