# staffops-anomaly-detection — Claude Code Guide

Distributed anomaly detection for Kubernetes: **Go controller + Go workers + Python ML service**.
Detects statistical anomalies (EWMA Z-Score), static threshold breaches, log rate spikes, and
ML-confirmed multivariate failures. Dispatches to Alertmanager (dry-run by default).

> **Status**: v0.7.0 — MVP complete. Core detection functional. 25 production blockers tracked
> (PH.1–PH.25). Do NOT deploy to cluster yet. Work scoped to `controller/` and `ml/` modules.

---

## Architecture

```
Controller (Go, :8080) ──gRPC batch──► Workers (Go, :50052) × 3
      │                                      │
      │  ◄── anomalies ─────────────────────┘
      │
      ├── Correlate + Deduplicate (Redis TTL 5min)
      ├── Enrich (VM + Loki queries, cached 30s)
      ├── ML evaluate (gRPC → Python :50051, if ≥2 correlated)
      └── Dispatch (Alertmanager or dry-run log)

Backing: Redis (baselines, dedup), VictoriaMetrics (PromQL), Loki (LogQL)
```

### Detection methods (in order of execution)

| Method | Algorithm | Fires on |
|--------|-----------|----------|
| Static | `value OP threshold` | Known limits (restarts > 3, CPU > 90%) |
| Adaptive | EWMA Z-Score > 3.0 | Unexpected deviation from learned baseline |
| Log pattern | LogQL rate query | Error rate spike, panic/OOM patterns |
| ML multivariate | Isolation Forest | ≥2 correlated anomalies, 10-feature canonical vector |

### Severity escalation

- **info**: Single signal, low confidence
- **warning**: Z-Score > 3 OR static breach
- **critical**: Metrics + logs both anomalous, OR ML confirms, OR ≥3 sibling pods simultaneously

---

## Build, test, run — ALL via Docker

**No local Go or Python.** Every operation runs in a container.

```bash
# Go — build both binaries
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine sh -c \
  "CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ && \
   CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/"

# Go — tests
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine go test ./...

# Go — tests with coverage (must stay ≥90% — currently ~35%, PH.9 blocker)
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine sh -c \
  "go test ./... -coverprofile=cov.out && go tool cover -func=cov.out | tail -5"

# Python ML — build image
docker build -t staffops-anomaly-ml ./ml

# Python ML — tests (empty, 0% coverage — PH.10 blocker)
docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
  "pip install -e '.[dev]' -q && pytest tests/ -v"

# Full local stack (controller + 3 workers + redis + ml + prometheus + grafana + loki)
cd scripts
cp ../.env.example .env    # then fill VM_URL, LOKI_URL, ALERTMANAGER_URL
./start.sh                 # build + docker-compose up
./monitor.sh               # real-time TUI dashboard
./stop.sh                  # cleanup

# Live logs
docker compose -f scripts/docker-compose.yaml logs -f controller
docker compose -f scripts/docker-compose.yaml logs -f worker
```

---

## Repository layout

```
controller/
  cmd/controller/main.go      ← Detection cycle orchestrator (60s ticker, gRPC dispatch)
  cmd/worker/main.go          ← gRPC server: query execution + detection algorithms
  config.yaml                 ← Main config with ${ENV_VAR:default} substitution
  internal/
    alert/                    ← Alertmanager dispatcher + deep link builder
    baseline/                 ← EWMA/Welford stats, Redis-backed, seasonal profiles
    config/                   ← YAML loader, env var expansion, file watcher (hot-reload)
    correlation/              ← Dedup, workload grouping, severity escalation
    detection/                ← Static, adaptive, pattern detection engines
    enrichment/               ← Pod/service context queries + template substitution
    ingestion/                ← VictoriaMetrics PromQL + Loki LogQL clients
    leader/                   ← K8s Lease-based leader election (HA, single-leader)
    ml/                       ← gRPC client + BuildFeatureVector()
    metrics/                  ← Prometheus registry (staffops_ad_* prefix)
    readiness/                ← Health probe aggregator (VM, Loki, AM, ML)
    redis/                    ← Connection pool
    replay/                   ← Offline simulation engine (12/16 tasks done)
    ratelimit/                ← Token bucket: VM 20/s, Loki 50/s
    suppression/              ← Namespace CSV filtering
  proto/                      ← worker.proto + ml.proto + generated stubs

ml/
  server/main.py              ← gRPC server, Prometheus :8082, health
  server/forecaster.py        ← Prophet wrapper (ready, not wired in controller yet)
  server/multivariate.py      ← Isolation Forest, CANONICAL_FEATURES padding (10 fixed)
  server/generated/           ← ml_pb2.py + ml_pb2_grpc.py

scripts/
  docker-compose.yaml         ← Full local stack (8 services)
  start.sh / stop.sh          ← Build + up/down
  monitor*.sh                 ← TUI dashboards
  observability/              ← prometheus.yml, loki-config.yaml, grafana provisioning

docs/                         ← MkDocs Material site (deploys to staffops.github.io/anomaly-detection/)
  architecture/               ← components, data-flow, decisions, degradation-model
  detection/                  ← static, adaptive, logs, ml, correlation
  configuration/              ← rules, suppression, enrichment
  operations/                 ← quickstart, replay, monitoring, troubleshooting
  reference/                  ← metrics.md, alert-rules.md (helm.md planned, not yet)
```

---

## Key data structures

```go
// detection.Anomaly — emitted by workers
type Anomaly struct {
    MetricName string
    Labels     map[string]string  // pod, namespace, service_name, ...
    Value      float64            // current reading
    Mean       float64            // EWMA baseline mean
    Stddev     float64            // Welford stddev
    Score      float64            // Z-Score distance
    Severity   string             // "critical" | "warning" | "info"
    Signal     string             // "metrics" | "logs"
    Detector   string             // "static" | "adaptive" | "pattern"
    Timestamp  time.Time
}

// correlation.CorrelatedAlert — assembled by controller
type CorrelatedAlert struct {
    Namespace   string
    Workload    string             // extracted via ExtractWorkload() — NEVER raw pod name
    Kind        string             // "pod" | "workload"
    Severity    string
    Anomalies   []Anomaly
    Enrichment  enrichment.Bundle  // CPU ratio, memory ratio, restarts_5m, error_rate_1m, ...
    MLDetection *MLDetection       // nil if ML skipped
}

// ML feature vector — 10 canonical fields, missing padded with 0.0
// anomaly_score, anomaly_value, cpu_ratio, memory_ratio, restarts_5m,
// error_rate_1m, latency_p99_5m, request_rate_1m, ready_replicas, oom_kills
```

---

## Configuration

Main config at `controller/config.yaml` — supports `${ENV_VAR:default}` substitution.
**Never hardcode endpoints, namespaces, or org-specific values** — always use env vars.

### Required environment variables

| Variable | Purpose |
|----------|---------|
| `VM_URL` | VictoriaMetrics query endpoint |
| `LOKI_URL` | Loki API endpoint |
| `ALERTMANAGER_URL` | Alertmanager API endpoint |

### Optional (defaults in parentheses)

| Variable | Default |
|----------|---------|
| `CLUSTER_NAME` | `unknown` |
| `REDIS_ADDR` | `redis:6379` |
| `REDIS_PASSWORD` | `` (no auth — PH.4 blocker) |
| `ML_ENDPOINT` | `ml:50051` |
| `ML_ENABLED` | `true` |
| `WORKER_ENDPOINT` | `dns:///worker:50052` |
| `LEADER_ELECTION_ENABLED` | `false` |
| `POD_NAME` | `` (downward API, leader identity) |
| `EXCLUDE_NAMESPACES_CSV` | `kube-system` |
| `EXCLUDE_STATIC_ONLY_CSV` | `` (batch ns: suppress static, allow adaptive) |
| `GRAFANA_BASE_URL` | `` (empty = no deep links) |
| `TEMPO_BASE_URL` | `` |
| `LOKI_BASE_URL` | `` |
| `RUNBOOK_BASE_URL` | `` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `` (OTel tracing, optional) |

Config hot-reload is active — editing `config.yaml` applies new detection rules on next cycle,
no restart needed.

### Adding a new detection rule

**Static** (`detection.static_rules` in config.yaml):
```yaml
- name: my_rule
  query: "max(some_metric{namespace!='kube-system'})"
  threshold: 10
  operator: ">"      # >, <, >=
  severity: warning
```

**Adaptive** (`detection.adaptive_metrics`):
```yaml
- name: my_metric
  query: "avg(some_metric) by (pod, namespace)"
  group_by: [pod, namespace]
```

**Log pattern** (`detection.log_patterns`):
```yaml
- name: error_spike
  query: "sum(rate({namespace=~\".+\"} |= \"error\" [5m])) by (namespace)"
  group_by: [namespace]
  type: rate
```

---

## Metrics (Prometheus)

All metrics prefixed `staffops_ad_`. Key ones:

| Metric | What it tells you |
|--------|-------------------|
| `_cycles_total{result}` | Detection cycles (success/error) |
| `_cycle_duration_seconds` | How long each cycle takes |
| `_anomalies_total{severity,signal}` | Anomalies found per signal type |
| `_alert_fired_total{severity}` | Alerts dispatched (or dry-run) |
| `_alert_deduplicated_total` | Alerts suppressed by 5min cooldown |
| `_is_leader` | 1 if this controller replica is leader |
| `_baseline_series_tracked` | EWMA series in Redis (cardinality watch) |
| `_ml_calls_total{method,status}` | ML gRPC call outcomes |

**Cardinality rules**: Labels must be bounded. `workload` uses `ExtractWorkload()` (never raw
pod name). Add labels only if they are in {severity, signal, detector, kind, namespace, workload}.

---

## Production blockers (do not ignore)

Work on these in the order listed. Never mark a blocker done without validating it.

### Critical (Kyverno hard-fails — cluster deploy blocked)
- **PH.1** `securityContext` missing: add `runAsNonRoot`, `readOnlyRootFilesystem`, `drop:[ALL]`
- **PH.2** `:latest` tag + `REPLACE_ME_REGISTRY` in manifests → CI-driven SHA tag
- **PH.4** Redis no auth → External Secrets Operator + mounted password
- **PH.5** ML Dockerfile: add multi-stage build (strip compiler from prod image)

### Test gates (CI blocked)
- **PH.9** Go coverage 35% → ≥90% (dispatcher, correlator, enrichment, suppression, redis, ratelimit, baseline, ml/client, config all need tests)
- **PH.10** ML coverage 0% → ≥90% (`ml/tests/` is empty)
- **PH.11** Failing test: `replay/window_test.go::TestParseWindow_MixedDurationAndTimestamp`

### Before calling it production-ready
- **PH.15** Create Helm chart (needed by GitOps deploy)
- **PH.19** Replace `prometheus.io/scrape` with VMServiceScrape CRDs
- **PH.24** Bump `grpcio` 1.62.1 → 1.65.x (CVE-2024-7246 DoS)

---

## Known bugs and sharp edges

| Issue | Where | Impact | Fix |
|-------|-------|--------|-----|
| Baseline resets on pod restart | `baseline/store.go` — key includes pod name | Cold-start after every rollout | P2.8: use workload-stable key |
| Baseline poisoning | `store.go::Evaluate()` — EWMA updated even on anomalous samples | Slow drift → FP erosion | P2.9: skip update when anomaly fired |
| Enrichment cache hit = 0% | `enrichment/engine.go` | No caching benefit today | Planned removal |
| FP rate from multiple comparisons | ~400 adaptive series, z > 3 → ~1000 FP/day | Alert fatigue | P0.4: FDR (Benjamini-Hochberg) |
| ML feature dimension mismatch | Fixed with CANONICAL_FEATURES (10 fixed, 0.0 padding) | Was causing ~33% errors | Done — monitor score distribution |
| Absence-of-signal undetected | No tracking of expected emission rate | Blind to metric dropout | P2.10 |
| Prophet not wired | `ml/forecaster.py` ready, controller doesn't call Forecast | No predictive alerts | Needs baseline time-series export first |

---

## Conventions

- **All builds via Docker** — no local Go/Python/pip
- **Config-as-code** — static rules in `config.yaml`, never in code
- **Dry-run is default** — `--dry-run` flag must be explicitly removed for production
- **Org-neutral binary** — no org-specific names in code; all endpoints via env vars
- **Cardinality-first** — review label sets for all new metrics before adding
- **Test independence** — tests written by a different agent/session than implementation
- **Coverage gate** — ≥90% enforced; below 90% = build fails
- **Conventional commits** — `feat/fix/docs/test/chore(scope): description`
- **Never commit** without explicit user approval
- **In-code docs in English** (comments, identifiers, log strings)

---

## Replay mode (offline validation)

Use replay before any config change reaches the cluster:

```bash
# Build controller first, then:
./bin/controller --replay \
  --from=24h \
  --config=controller/config.yaml \
  --output=report.json

# Output: report.json + report.md (sparklines, anomaly tables, execution stats)
# Zero side effects: no Redis writes, no Alertmanager calls, no gRPC fan-out
```

Status: 12/16 tasks done (CLI complete, smoke-tested). Integration test (T13) and
smoke test against real endpoints (T14) still pending.

---

## Ports

| Service | Port | Protocol | Purpose |
|---------|------|----------|---------|
| Controller | 8080 | HTTP | Prometheus metrics + readiness probe |
| Worker | 50052 | gRPC | Job processing |
| Worker | 8081 | HTTP | Prometheus metrics |
| ML service | 50051 | gRPC | Forecast + DetectMultivariate |
| ML service | 8082 | HTTP | Prometheus metrics |
| Redis | 6379 | TCP | Baselines + dedup |

---

## Docs site

MkDocs Material — deploys to `https://staffops.github.io/anomaly-detection/` via GitHub Actions
on push to `main`. Local preview:

```bash
# Requires mkdocs-material installed
mkdocs serve -f mkdocs.yml
```
