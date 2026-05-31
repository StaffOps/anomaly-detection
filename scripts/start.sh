#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTROLLER_DIR="$(dirname "$SCRIPT_DIR")/controller"

echo "=== Building Go binaries ==="
docker run --rm -v "$CONTROLLER_DIR":/src -w /src golang:1.22-alpine sh -c \
  "CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ && CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/"

echo "=== Starting full stack ==="
cd "$SCRIPT_DIR"
docker compose up -d --build

echo ""
echo "=== Running ==="
echo "Controller metrics:  http://localhost:8080/metrics"
echo "ML service gRPC:     localhost:50051"
echo "ML service metrics:  http://localhost:8082"
echo ""
echo "Logs:    docker compose -f $SCRIPT_DIR/docker-compose.yaml logs -f"
echo "Stop:    $SCRIPT_DIR/stop.sh"
