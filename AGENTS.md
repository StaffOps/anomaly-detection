# staffops-anomaly-detection — Agent Guide

> Canonical onboarding guide for any AI coding agent (Claude Code, Cursor, Codex,
> Gemini CLI, Copilot, …). Tool-specific files (e.g. `CLAUDE.md`) point here.

Distributed anomaly detection for Kubernetes: **Go controller + Go workers + Python ML service**.
Detects statistical anomalies (EWMA Z-Score), static threshold breaches, log rate spikes, and
ML-confirmed multivariate failures. Dispatches to Alertmanager (dry-run by default).

> **Status**: v0.11.0 — deployed to **homolog** (cluster `devops-core`, namespace `staffops`,
> **dry-run**) via the Helm chart. Core detection + FDR + ML live. Since 0.7.0:
> **F0** (FDR now corrects over the full test family, not the censored fired set — it
> actually rejects now), **direction-of-badness** (adaptive rules only fire in the
> declared bad direction), **rule hygiene** (dead rules dropped, `rate()` windows → `[2m]`,
> repo↔chart drift resolved on devops-core), a tuned 18-rule set + Group A rules
> (unbiased RED via OTel SDK http metrics, DB latency, CPU throttling, service-graph
> self-health), and a full VictoriaMetrics→Prometheus terminology sweep.
> Remaining: exit dry-run, CI-published images, the P0.1 recall/FP measurement.
> Trunk-based on `main` (branch `dev` retired 2026-07-13).

---

## Architecture

```
Controller (Go, :8080) ──gRPC batch──► Workers (Go, :50052) × 3
      │                                      │
      │  ◄── anomalies ─────────────────────┘
      │
      ├── Correlate + Deduplicate (Redis TTL 5min)
      ├── Enrich (Prometheus + Loki queries, cached 30s)
      ├── ML evaluate (gRPC → Python :50051, if ≥2 correlated)
      └── Dispatch (Alertmanager or dry-run log)

Backing: Redis (baselines, dedup), Prometheus (PromQL), Loki (LogQL)
```

### Detection methods (in order of execution)

| Method | Algorithm | Fires on |
|--------|-----------|----------|
| Static | `value OP threshold` | Known limits (restarts > 3, CPU > 90%) |
| Adaptive | EWMA Z-Score > 3.0 | Unexpected deviation from learned baseline |
| Log pattern | LogQL rate query | Error rate spike, panic/OOM patterns |
| ML multivariate | Isolation Forest | ≥2 correlated anomalies, 10-feature canonical vector |

**Adaptive post-filters (before dispatch):**
- **Direction-of-badness** — adaptive rules carry `direction: up_bad|down_bad|both_bad`
  (empty = `both_bad`). The z-score is symmetric (`|z|`), so the controller drops firings
  that ran the harmless way (e.g. latency *improving*) before FDR. Metric:
  `staffops_ad_detection_direction_filtered_total`.
- **FDR (Benjamini-Hochberg)** — corrects for multiple comparisons across the *full* family
  of adaptive evaluations this cycle (workers report `adaptive_series_tested`; the controller
  passes it to BH). Only adaptive anomalies are filtered; static/pattern pass through.

### Severity escalation

- **info**: Single signal, low confidence
- **warning**: Z-Score > 3 OR static breach
- **critical**: Metrics + logs both anomalous, OR ML confirms, OR ≥3 sibling pods simultaneously

---

## Build, test, run — ALL via Docker

**No local Go or Python.** Every operation runs in a container.

```bash
# staffops-otel-libs is a public org module — Go pulls it via the default proxy,
# no token / GOPRIVATE / git config needed.

# Go — build both binaries
# staffops-otel-libs is a public org module now — no token / GOPRIVATE needed.
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.25-alpine sh -c '
  CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ &&
  CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/'

# Go — tests
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.25-alpine go test ./...

# Go — tests with coverage (≥90% gate is PH.9 — done; CI test-go enforces it)
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.25-alpine sh -c '
  go test ./internal/... -coverprofile=cov.out && go tool cover -func=cov.out | tail -5'

# Python ML — build image
docker build -t staffops-anomaly-ml ./ml

# Python ML — tests (PH.10 done — ~98% coverage)
# otel_helper isn't on PyPI and core-requires protobuf>=5.0 (OTLP-grpc),
# incompatible with this service's protobuf==4.25.3 pin — installed
# --no-deps; see ml/pyproject.toml for the full rationale.
docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
  "pip install -e '.[dev]' -q && \
   pip install --no-deps -q https://github.com/StaffOps/staffops-otel-libs/releases/download/v0.2.0/otel_helper-0.2.0-py3-none-any.whl && \
   pytest tests/ -v"

# Full local stack (controller + 3 workers + redis + ml + prometheus + grafana + loki)
cd scripts
cp ../.env.example .env    # then fill PROMETHEUS_URL, LOKI_URL, ALERTMANAGER_URL
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
    ingestion/                ← Prometheus PromQL + Loki LogQL clients
    leader/                   ← K8s Lease-based leader election (HA, single-leader)
    ml/                       ← gRPC client + BuildFeatureVector()
    metrics/                  ← Prometheus registry (staffops_ad_* prefix)
    readiness/                ← Health probe aggregator (Prometheus, Loki, AM, ML)
    redis/                    ← Connection pool
    replay/                   ← Offline simulation engine (12/16 tasks done)
    ratelimit/                ← Token bucket: Prometheus 20/s, Loki 50/s
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

> **Deploy vs local rule set (Decision 10)**: `controller/config.yaml` here is
> **for local testing (docker-compose) and `--replay` only**. The **deployed**
> detection rules — what runs on the cluster — live in the per-cluster Helm values
> override (`k8s-setup/.../anomaly-detection/values.yaml.gotmpl`, `detection:`) — that
> gotmpl is the source of truth. Editing `config.yaml` affects local/replay only, not
> the cluster. They diverge **by design**. ⚠️ **If unsure which file to edit for a
> rule change, ask first — don't guess.** See `docs/site/architecture/decisions.md`
> Decision 10.

### Required environment variables

| Variable | Purpose |
|----------|---------|
| `PROMETHEUS_URL` | Prometheus query endpoint |
| `LOKI_URL` | Loki API endpoint |
| `ALERTMANAGER_URL` | Alertmanager API endpoint |

### Optional (defaults in parentheses)

| Variable | Default |
|----------|---------|
| `CLUSTER_NAME` | `unknown` |
| `REDIS_ADDR` | `redis:6379` |
| `REDIS_PASSWORD` | `` (PH.4 done — synced from AWS Secrets Manager via ESO when `redis.auth.enabled`) |
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
  direction: up_bad   # up_bad | down_bad | both_bad (default). Only fire when
                      # the metric moves the bad way (latency/errors → up_bad).
  min_value: 20       # optional absolute floor. The z-score is scale-free, so a
                      # gauge idling near zero scores 6–14σ for a few units —
                      # statistically anomalous, operationally noise. Fires only
                      # when z > threshold AND |value| ≥ min_value. 0 = no floor.
                      # Rejected at load if combined with direction: down_bad.
```

⚠️ **Check `group_by` against the labels the metric actually has.** If a label is absent,
PromQL's `by (...)` silently drops it and the alert comes out with an empty `namespace` —
unroutable. OTel SDK metrics and spanmetrics carry `service_namespace`/`eks_cluster`, **not**
`namespace`/`cluster`; map them in the query (`label_replace`, or `label_format` in LogQL),
because the aggregation discards the original label before the controller ever sees it.
Verify by running the query and confirming every `group_by` label comes back populated.

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
| `_detection_fdr_accepted_total` / `_fdr_rejected_total` | Adaptive anomalies kept / cut by Benjamini-Hochberg FDR |
| `_detection_fdr_family_size` | BH family size `m` (adaptive evaluations/cycle); ~0 ⇒ censored family (F0 regression) |
| `_detection_direction_filtered_total` | Adaptive anomalies dropped for firing in the harmless direction |
| `_detection_floor_filtered_total` | Adaptive anomalies dropped for not crossing the rule's `min_value` (near-zero-baseline noise) |
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
- **PH.1** ✅ *done* — images run as nonroot (`USER 65534`) and the Helm chart sets pod/container
  `runAsNonRoot`, `readOnlyRootFilesystem`, `drop:[ALL]` (validated in the deployed manifest).
- **PH.2** 🟡 *partial* — deployed image is a local Harbor build (`0.7.0-homolog-<sha>`), no
  `:latest`. Chart pulls registry/tag from values (no `REPLACE_ME`). **Remaining**: point at a
  CI-published versioned image and settle the canonical registry (Docker Hub CI vs Harbor deploy).
- **PH.4** ✅ *done* — in-cluster Redis AUTH via External Secrets Operator (ClusterSecretStore
  `aws`); chart renders `externalsecret-redis.yaml`, password injected as `REDIS_PASSWORD`.
- **PH.5** ✅ *done* — ML Dockerfile is multi-stage; runtime image has no gcc/grpcio-tools.

### Test gates (CI blocked)
- **PH.9** ✅ *done* — Go total coverage **90.4%**; `test-go` gate armed (blocking).
- **PH.10** ✅ *done* — ML test suite added, ~98% coverage.
- **PH.11** ✅ *done* — `replay/window_test.go` and full Go suite pass (validated).

### Before calling it production-ready
- **PH.15** ✅ *done* — Helm chart is canonical in the `helm-charts` repo
  (`charts/anomaly-detection`) and deployed to homolog. Carries the PH.1 securityContext.
- **PH.19** ✅ *done* — chart renders `ServiceMonitor` (enabled on the Prometheus monitoring stack).
- **PH.24** ✅ *done* — runtime `grpcio` bumped to 1.65.4 (CVE-2024-7246). `grpcio-tools`
  stays 1.62.1 (build-time only; avoids forcing protobuf 5.x + stub regen).

### Remaining before production
- **Exit dry-run** — controller runs `--dry-run`; ~12.8k alerts/day in homolog. FDR runs
  before correlation but historically rejected ~0 because it was handed a *censored* family
  (only the anomalies that fired, not the full ~400 tests/cycle). Fixed in F0 (2026-07-17):
  workers now report `adaptive_series_tested` and the controller passes the true family size
  to BH — measure the new rejection rate in replay before flipping `controller.dryRun: false`.
- **CI-published image** (PH.2 tail) + settle canonical registry.

---

## Known bugs and sharp edges

| Issue | Where | Impact | Fix |
|-------|-------|--------|-----|
| Baseline resets on pod restart | `baseline/store.go` | Cold-start after every rollout | ✅ P2.8: workload-stable keying |
| Baseline poisoning | `store.go::Evaluate()` — EWMA drift on anomalous samples | Slow FP erosion | ✅ P2.9: anti-poison gate skips update on extreme anomalies |
| Enrichment cache hit = 0% | `enrichment/engine.go` | No caching benefit today | Planned removal |
| FP rate from multiple comparisons | ~400 adaptive series, z > 3 | Alert fatigue | ✅ F0 (2026-07-17): FDR now corrects over the full family — workers report `adaptive_series_tested`, controller passes it to BH. Was rejecting ~0 due to a censored (fired-only) family, not pipeline position. Measure rejection rate in replay before exit-dry-run |
| ML feature dimension mismatch | Fixed with CANONICAL_FEATURES (10 fixed, 0.0 padding) | Was causing ~33% errors | ✅ Done — monitor score distribution |
| Absence-of-signal undetected | `absence/tracker.go` — dead man's switch | Blind to metric dropout | ✅ P2.10: shipped |
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
- **Docs move with code (pre-commit gate)** — any change to behaviour, config, or
  metrics MUST update the matching page under `docs/site/**` and `CHANGELOG.md` in the
  same commit. Enforced by `.githooks/pre-commit` (enable with
  `git config core.hooksPath .githooks`; `scripts/dev/doctor.sh` wires it automatically).
  A code-only commit is blocked unless you bypass with `git commit --no-verify`
  (reserve that for pure refactors/tests). New knob → `configuration/`; new metric →
  `reference/metrics.md`; new Helm value → `reference/helm.md`.

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

## CI/CD (GitHub Actions)

Five workflows in `.github/workflows/` (org-standard layout, mirrors staffops-aigent-squad):

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `test.yml` | push/PR `main` | `lint-go` (gofmt+vet), `lint-ml` (ruff), `dep_scan` (Trivy fs), `test-go` + `test-ml` |
| `sast.yml` | push/PR `main` | `gosec` (Go) + `bandit` (ML) |
| `build.yml` | push `main` | per image: build local → Trivy scan → push to Docker Hub `<img>:{sha,latest}` → SBOM |
| `release.yml` | tag `v*` / manual | versioned immutable images + GitHub Release |
| `docs.yml` | push/PR `main` | PR builds `--strict`; main deploys to gh-pages |

- **Registry**: **Docker Hub** (not ghcr) — the repo is private, so images are published to
  the org's Docker Hub account (same as staffops-aigent-squad). Needs the
  `DOCKERHUB_USERNAME` + `DOCKERHUB_TOKEN` secrets. Images:
  `<user>/staffops-anomaly-detection-{controller,worker,ml}`.
- **Go module `staffops-otel-libs`**: public org module
  (`github.com/staffops/staffops-otel-libs/go`, tagged `v0.1.0`) — pulled via the
  default Go proxy. No token, no GOPRIVATE, no SSH.
- **Rollout = report-only gates**: `lint-*`, `dep_scan`, `gosec`, `bandit` run
  `continue-on-error`, and the Trivy image scan is `exit-code: 0`. They surface
  pre-existing debt (gofmt, `go vet`, grpc/otel/base-image CVEs) without blocking `main`.
  Flip each back to blocking (`continue-on-error` off / `exit-code: 1`) as the debt clears.
  `lint-go` and the coverage gates (`test-go` at 90.4%, `test-ml`) are now **blocking** —
  PH.9/PH.10 landed.

---

## Docs site

MkDocs Material — deploys to `https://staffops.github.io/anomaly-detection/` via GitHub Actions
on push to `main`. Local preview:

```bash
# Requires mkdocs-material installed
mkdocs serve -f mkdocs.yml
```
