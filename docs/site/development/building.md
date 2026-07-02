# Building

## Go (Controller + Workers)

```bash
# Build both binaries
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.25-alpine sh -c \
  "CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ && \
   CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/"
```

Output: `controller/bin/controller` and `controller/bin/worker` (static binaries).

## Python (ML Service)

```bash
# Build Docker image
docker build -t staffops-anomaly-ml ./ml
```

## Full Stack (docker-compose)

```bash
# Build + start everything
./scripts/start.sh

# Or manually:
docker compose -f scripts/docker-compose.yaml up --build -d
```

## Docker Images

| Image | Base | Size |
|-------|------|------|
| Controller | `alpine:3.20` | ~15MB |
| Worker | `alpine:3.20` | ~15MB |
| ML Service | `python:3.11-slim` | ~400MB |

### Controller/Worker Dockerfile

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/controller ./cmd/controller/
RUN CGO_ENABLED=0 go build -o /bin/worker ./cmd/worker/

FROM alpine:3.20
COPY --from=builder /bin/controller /bin/worker /usr/local/bin/
ENTRYPOINT ["controller"]
```

### ML Dockerfile

```dockerfile
FROM python:3.11-slim
WORKDIR /app
COPY pyproject.toml .
RUN pip install --no-cache-dir -e .
COPY . .
CMD ["python", "-m", "server.main"]
```

## Multi-Architecture

For production images (amd64 + arm64):

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t registry/staffops-anomaly-controller:v0.7.0 \
  ./controller
```

!!! note "Local development"
    Local builds are single-arch (your machine's architecture). Multi-arch is only needed for cluster deployment.
