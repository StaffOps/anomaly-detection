FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends gcc g++ && rm -rf /var/lib/apt/lists/*

COPY pyproject.toml .
RUN pip install --no-cache-dir \
    grpcio==1.62.1 \
    grpcio-tools==1.62.1 \
    protobuf==4.25.3 \
    prophet==1.3.0 \
    scikit-learn==1.4.2 \
    numpy==1.26.4 \
    pandas==2.2.1 \
    prometheus-client==0.20.0

COPY proto/ proto/
COPY server/ server/
RUN python -m grpc_tools.protoc -I proto --python_out=server/generated --grpc_python_out=server/generated proto/ml.proto && \
    sed -i 's/^import ml_pb2 as ml__pb2/from server.generated import ml_pb2 as ml__pb2/' server/generated/ml_pb2_grpc.py

EXPOSE 50051 8082

CMD ["python", "-m", "server.main"]
