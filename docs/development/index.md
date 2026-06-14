# Development

## Overview

The project uses Go 1.22 (controller/workers) and Python 3.11 (ML service). **All builds run via Docker** — no local language SDKs required.

## Prerequisites

- Docker and Docker Compose
- Git
- `bash`, `curl`, `jq` (for scripts)

## Project Layout

```
controller/
├── cmd/
│   ├── controller/main.go    # Controller entrypoint
│   └── worker/main.go        # Worker entrypoint
├── internal/                  # All business logic (not importable)
│   ├── baseline/              # EWMA + Welford statistics
│   ├── correlation/           # Dedup, workload grouping
│   ├── detection/             # Detection engine
│   ├── enrichment/            # Context queries
│   ├── ingestion/             # VM + Loki clients
│   ├── metrics/               # Prometheus instrumentation
│   ├── ml/                    # ML gRPC client
│   ├── readiness/             # Health probes
│   └── replay/                # Offline replay engine
├── proto/                     # Protobuf definitions
├── config.yaml                # Main configuration
├── go.mod / go.sum
└── Dockerfile

ml/
├── server/
│   ├── main.py                # gRPC server entrypoint
│   ├── forecaster.py          # Prophet forecasting
│   └── multivariate.py        # Isolation Forest
├── proto/                     # Protobuf source
├── pyproject.toml
└── Dockerfile
```

## Guides

- [**Building**](building.md) — Compile, build images, run locally
- [**Testing**](testing.md) — Unit tests, integration tests, coverage
- [**Contributing**](contributing.md) — Conventions, workflow, code quality
