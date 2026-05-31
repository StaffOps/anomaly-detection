# staffops-anomaly-detection / ml

Python ML service for anomaly detection. Provides Prophet forecasting and Isolation Forest multivariate detection via gRPC.

## Interface

Defined in `proto/ml.proto`:

| RPC | Purpose |
|-----|---------|
| `Forecast` | Prophet time-series forecasting with breach prediction |
| `DetectMultivariate` | Isolation Forest on multiple metrics simultaneously |
| `Health` | Readiness check |

## Quick Start

```bash
# Generate proto
pip install -e '.[dev]'
python -m grpc_tools.protoc -I proto --python_out=server/generated --grpc_python_out=server/generated proto/ml.proto

# Run
python -m server.main
```

## Docker

```bash
docker build -t staffops-anomaly-ml .
docker run -p 50051:50051 -p 8082:8082 staffops-anomaly-ml
```

## Ports

| Port | Purpose |
|------|---------|
| 50051 | gRPC server |
| 8082 | Prometheus metrics |

## Related

- [Controller](../controller) — Go controller + workers that call this service
