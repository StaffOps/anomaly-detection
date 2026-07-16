# ─── builder: compile protos + install deps (includes gcc for C extensions) ──
FROM python:3.11-slim AS builder

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ curl && rm -rf /var/lib/apt/lists/*

# Install all deps including grpcio-tools (needed for proto compilation).
# grpcio (runtime, C-core) bumped to 1.65.4 for CVE-2024-7246 (DoS) fix.
# grpcio-tools stays at 1.62.1: it is build-time only (not shipped in the
# runtime image) and 1.65.x would force protobuf>=5.26.1, requiring a stub
# regeneration we don't need. 1.62.1 is compatible with protobuf 4.25.3.
RUN pip install --no-cache-dir \
    grpcio==1.65.4 \
    grpcio-tools==1.62.1 \
    protobuf==4.25.3 \
    prophet==1.3.0 \
    scikit-learn==1.4.2 \
    numpy==1.26.4 \
    pandas==2.2.1

# otel_helper's Prometheus reader — the only piece we use (metric_exporters=
# ["prometheus"]). "otel-helper"'s own metadata core-requires
# opentelemetry-exporter-otlp-proto-grpc, which needs protobuf>=5.0 and would
# collide with the protobuf==4.25.3 pin above, so install it --no-deps and
# pull in only the three OTel packages actually exercised in this mode.
# Every other otel_helper feature (OTLP export, auto-instrumentation) is
# wrapped in try/except ImportError and degrades cleanly without its package
# installed — see ml/pyproject.toml for the full rationale. pip requires the
# canonical wheel filename (renaming it fails with "not a valid wheel
# filename"), so -O keeps the upstream name instead of -o.
RUN pip install --no-cache-dir \
      "opentelemetry-api>=1.42,<2" \
      "opentelemetry-sdk>=1.42,<2" \
      "opentelemetry-exporter-prometheus>=0.63b0" && \
    curl -fsSL -O \
      https://github.com/StaffOps/staffops-otel-libs/releases/download/v0.2.0/otel_helper-0.2.0-py3-none-any.whl && \
    pip install --no-cache-dir --no-deps otel_helper-0.2.0-py3-none-any.whl && \
    rm otel_helper-0.2.0-py3-none-any.whl

COPY proto/ proto/
COPY server/ server/

# Compile proto stubs in builder stage so the runtime image only needs
# the generated files — not grpcio-tools or gcc.
RUN python -m grpc_tools.protoc \
    -I proto \
    --python_out=server/generated \
    --grpc_python_out=server/generated \
    proto/ml.proto && \
    sed -i 's/^import ml_pb2 as ml__pb2/from server.generated import ml_pb2 as ml__pb2/' \
    server/generated/ml_pb2_grpc.py

# ─── runtime: slim image without compiler or build tools ─────────────────────
FROM python:3.11-slim

WORKDIR /app

# Copy installed Python packages from builder (no gcc in runtime).
# python:3.11-slim installs into site-packages (not dist-packages).
COPY --from=builder /usr/local/lib/python3.11/site-packages \
                    /usr/local/lib/python3.11/site-packages
# Console scripts installed by deps (e.g. grpc/proto helpers) live here.
COPY --from=builder /usr/local/bin /usr/local/bin
COPY --from=builder /app/server /app/server

# nobody (65534) has home /nonexistent (read-only); matplotlib (via prophet)
# needs a writable cache dir. Point it at /tmp to avoid a noisy startup warning.
ENV MPLCONFIGDIR=/tmp/matplotlib

# Run as nobody (65534) — satisfies runAsNonRoot Kyverno policy.
# Debian slim has nobody at 65534.
USER 65534

EXPOSE 50051 8082

CMD ["python", "-m", "server.main"]
