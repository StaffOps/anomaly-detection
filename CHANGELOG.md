# Changelog

All notable changes to this project per [Keep a Changelog](https://keepachangelog.com/) and [Semantic Versioning](https://semver.org/).

Versioning is **milestone-based**, not commit-based. Each component (`controller/`, `ml/`) is versioned independently. See `staffops_agent_definition/steering/version-management.md` for the full policy.

---

## [Unreleased]

### replay

**Fixed — replay in-mem baseline under-detected vs production (P0.1 blocker, 2026-07-19)**
- `internal/replay/inmem_baseline.go` computed the z-score **after** folding the
  sample into EWMA/stddev (dampened numerator — the EWMA already moved toward the
  value — and a denominator inflated by the spike itself) and had **no stddev
  floor** — unlike production (`internal/baseline/store.go`), which detects against
  the **prior** baseline with a floor. Replay therefore under-detected, so injected
  and real faults under-fired. Rewritten to mirror production exactly (z on
  pre-update stats + floor + anti-poison gate); the warm-up off-by-one is aligned too
  (`stats.Count < WarmUpSamples`). Test: `TestInMemStore_SpikeFiresAfterWarmup`.
- **Consequence**: prior replay numbers came from the under-detecting path and are
  not trustworthy — remeasure. This unblocks the P0.1 recall/FP measurement
  (`specs/synthetic-injection/`).
- The replay markdown report now renders an **Injection Scoring** block
  (precision/recall/F1, TP/FP/FN, recall-by-type) — previously JSON-only.

## controller — [0.11.0] — 2026-07-18

Detection quality + FP control milestone. Consolidates the FDR full-family fix (F0),
direction-of-badness, rule hygiene, the tuned deployed rule set with Group A additions
(unbiased RED / DB latency / CPU throttling / service-graph self-health), and a full
VictoriaMetrics→Prometheus terminology sweep. Homologated on `devops-core` (dry-run).

### detection

**Added — direction-of-badness per adaptive rule (v0.11, 2026-07-18)**
- The adaptive detector fires on `|z|` (symmetric), so it alerted even when a
  metric moved the *harmless* way (latency dropping, error rate falling). New
  optional `direction:` field on adaptive rules — `up_bad` | `down_bad` |
  `both_bad` (empty = `both_bad`, backward-compatible). The controller drops
  wrong-direction adaptive anomalies before FDR (`internal/detection/direction.go`,
  derived from Value vs Mean — no proto/worker change). Metric:
  `staffops_ad_detection_direction_filtered_total`. Most service RED rules are
  now `up_bad`; `request_rate` stays `both_bad` (a traffic drop can be an outage).

**Added — Group A rules: unbiased RED + root-cause + pipeline self-health (v0.11)**
- Deployed to devops-core via the gotmpl `detection:` override (all metrics
  verified present in the live inventory 2026-07-18):
  - **Unbiased RED** — `http_error_ratio_by_service`, `http_latency_p99_by_service`
    from the OTel SDK `http_server_request_duration_seconds_*` (100% of traffic,
    *not* sampling-biased like the `spanmetrics_apm_*` rules).
  - **Root cause** — `db_latency_p99_by_service` (`db_client_operation_duration_seconds`),
    the direct "is it the database?" signal.
  - **Leading indicator** — `cpu_throttling_ratio` (CFS throttling), which the
    `cpu_ratio` static misses (a pod can throttle hard at 60% "usage").
  - **Pipeline self-health** — `servicegraph_series_limited` (static) +
    `servicegraph_expired_edges` (adaptive) guard the Tempo metrics-generator;
    if it degrades, every trace-derived rule blinds silently.

**Changed — rule hygiene: 5 dead rules removed, rate() windows widened (2026-07-17)**
- Audited every `config.yaml` rule metric against the live Prometheus
  inventory. Removed rules whose metrics do not exist (they never fired):
  `dotnet_gc_pause_rate`, `queue_depth`, `queue_failed_rate`, `go_sql_waiting`,
  `karpenter_scheduling_duration` (Karpenter renamed the `provisioner_*` family
  upstream). `dotnet_threadpool_saturated` simplified — its
  `dotnet_thread_pool_queue_length_total` alternative (.NET 9+ meter) is not
  emitted by any workload; only the `process_runtime_dotnet_*` era exists.
- Widened `rate()` windows `[1m]` → `[2m]` on adaptive/log rules (org scrape
  interval is 30s; `rate()` needs ≥4× scrape to survive a missed scrape).
  Enrichment queries deliberately untouched (`*_1m` names are ML canonical
  feature names — renaming would break the feature-vector contract).
- **Drift finding → resolved for deploy (2026-07-18)**: the deployed rule set
  came from the Helm chart's small default `detection:` (incl. `high_cpu_ratio`/
  `high_memory_ratio`, retired in this repo for duplicating VMAlert), NOT this
  repo's tuned `config.yaml`. Ported the tuned 18-rule set into the devops-core
  gotmpl `detection:` override (6→18 rules; dropped the noisy cpu/mem statics).
  Homologated: 0 cycle errors, FDR family 277 (service-level vs ~2500 pod-level),
  rejection ~18%. Also bumped `controller.jobInterval` 30s→60s — the richer
  set's spanmetrics/istio histogram queries pushed cycle p99 to ~58s (backing up
  a 30s cadence); at 60s the steady-state p99 is ~20s. Whether the chart
  *default* should also carry the tuned set (SSOT) stays open (program map §7.8).

**Fixed — FDR (Benjamini-Hochberg) corrected over a censored family, rejecting ~0 (F0, 2026-07-17)**
- Root cause: `fdr.Apply` inferred the statistical family size from the anomalies
  it received, but workers only ship anomalies that already fired (z > threshold).
  BH over that truncated family — a handful of uniformly tiny p-values — accepts
  nearly everything by construction, so the correction was decorative (rejected ~0).
- Fix: workers now report `adaptive_series_tested` (the count of adaptive
  evaluations past warm-up) on `JobResults`; the controller passes it to
  `fdr.Apply(anomalies, totalTests)` as the true family size `m`. A marginal
  anomaly (e.g. z≈3.0) that passed against `m=1` is now correctly rejected once
  the cycle's ~400 tests are accounted for, while genuinely strong signals still
  survive. Falls back to the fired count when `totalTests` is unreported (≤ fired).
- New gauge `staffops_ad_detection_fdr_family_size` exposes `m` per cycle — a
  value near 0 means workers are not reporting tested series (censored family).
- Replay now mirrors the controller: FDR is applied per tick over the real family,
  and the report carries `fdr_rejected`. Also fixed a replay bug where every
  adaptive rule was evaluated against the pooled series of *all* rules
  (cross-metric baseline contamination absent from the production worker path).
- Proto change: `JobResults.adaptive_series_tested` (field 3). Stubs regenerated.

## controller — [0.10.1] — 2026-07-17

### observability

**Fixed — structured logs (`alert_fired`, `anomaly_detected`, ...) were silently discarded (2026-07-17)**
- Root cause: `staffops-otel-libs/go`'s `NewLogger` always bridged `slog` through
  the OTel Logs API into the process's `LoggerProvider`. When no
  `OTEL_EXPORTER_OTLP_ENDPOINT` is configured (this deployment's case),
  `configureLogging` builds that `LoggerProvider` with no export processor
  attached — routing records through it silently dropped every one, with no
  error anywhere. In practice: a full day of `alert_fired`/`anomaly_detected`
  logs never reached stdout or Loki, breaking dry-run alert auditing (the
  "🟢 Active alerts" / "📜 Recent alerts" Grafana panels showed nothing, not
  because of a bad query, but because the logs genuinely didn't exist).
  Bumped `staffops-otel-libs/go` 0.2.0 → 0.2.1, which fixes `NewLogger` to
  fall back to a plain stdout JSON handler when no processor was attached.
- `staffops_ad_worker_baseline_series_tracked` gauge was declared but never
  recorded anywhere — `baseline.Store` now tracks distinct baseline keys seen
  per-process and updates the gauge on each new series (cheap: only touches
  the metric on the rare new-series case, not the hot path).

## controller — [0.10.0] — 2026-07-16

### detection

**Fixed — propagate the monitored workload's real `cluster` through the detection pipeline (2026-07-16)**
- This deployment queries a federated Prometheus/Loki spanning multiple K8s clusters
  (`devops-core`, `applications-dev-nv`, `applications-prd-nv`, `applications-prd-sp`), but
  every static/adaptive/log-pattern query in `config.yaml` aggregated with
  `by(namespace, pod)` etc. **without** `cluster` — so the real cluster was dropped before an
  `Anomaly` was even built. All 7 `static_rules`, all 12 `adaptive_metrics`
  (`by()` + `group_by`), and both `log_patterns` now include `cluster`; enrichment
  `pod_bundle`/`service_bundle` query templates now match on `cluster="$cluster"` too.
  Discovered via the "Top noisy workloads" Grafana panel showing every row as `devops-core`
  even though the underlying workloads span all 4 clusters.
- Not a revert of the 0.9.0 change that removed the app's own constant `cluster` label wrap
  (that was the controller pod's *own* cluster identity, now scrape-layer-only via
  ServiceMonitor/vmagent). This is a different thing: the *monitored workload's* cluster,
  read out of the federated query results themselves, same as `namespace` already was.
- **Correctness bug this fixes**: the correlator's workload dedup key (`namespace/pod`) didn't
  include cluster, so two different clusters with an identically-named namespace+pod
  (plausible across `applications-*`) would incorrectly correlate/dedup as the same workload.
  `workloadKey()` and the workload-pattern key are now `cluster/namespace/workload`
  (`ExtractWorkloadFromKey` now returns the trailing segment instead of everything after the
  first `/`); the Redis dedup fingerprint now hashes `cluster` too.
- `enrichment.Identity` gained a `Cluster` field (populated from `cluster` /
  `k8s_cluster_name` / `k8s.cluster.name`, mirroring how `Namespace`/`Pod` resolve) and a
  `$cluster` template placeholder; `CorrelatedAlert` gained a `Cluster` field; the Alertmanager
  dispatcher now prefers the anomaly's own `cluster` label over the controller's `CLUSTER_NAME`
  (falling back to it only when an anomaly carries no cluster, e.g. the cluster-wide
  `karpenter_scheduling_duration` rule before broader rollout).
- `staffops_ad_detection_anomalies_by_workload_total` gained a `cluster` label — see
  [Metrics Reference](docs/site/reference/metrics.md#staffops_ad_detection_anomalies_by_workload_total).
  New cardinality: severity(3) × cluster(4) × namespace(~50) × workload(~20-50/ns) ≈ 12-28k
  series (single metric, not per-cluster-deployment — see the code comment in `metrics.go`).

## ml — [0.3.0] — 2026-07-16

### observability

**Changed — ML service metrics now route through `otel_helper` (Python) (2026-07-16)**
- All `staffops_ad_ml_*` instruments moved from direct `prometheus_client` usage to the OTel
  Metrics API (`otel_helper.get_meter`), served via `otel_helper`'s Prometheus reader on the
  existing `:8082/metrics` endpoint (`setup_telemetry(TelemetryOptions(metric_exporters=
  ["prometheus"], prometheus_metrics_port=8082))`, called from `serve()`). Metric names and
  label keys are unchanged; the histogram `.time()` context managers became manual
  `time.perf_counter()` deltas recorded in a `finally` block (same coverage, both success and
  error paths).
- `otel_helper` isn't on PyPI — installed from a GitHub Release wheel with `--no-deps`. Its
  core metadata mandates `opentelemetry-exporter-otlp-proto-grpc`, which requires
  `protobuf>=5.0` — incompatible with this service's `protobuf==4.25.3` pin (`grpcio-tools`
  build compat, see `Dockerfile`). Only the three `opentelemetry-*` packages actually
  exercised in Prometheus-only mode are installed explicitly; every other `otel_helper`
  feature (OTLP export, auto-instrumentation) degrades cleanly via its own
  `try/except ImportError` guards. Full rationale in `ml/pyproject.toml`. Traces/OTLP push are
  not wired up yet (tracked separately — sync gRPC server, `otel_helper` only auto-instruments
  `grpc.aio`).

## controller — [0.9.0] — 2026-07-16

### observability

**Changed — controller/worker metrics now route through `staffops-otel-libs/go` (2026-07-16)**
- Bumped `staffops-otel-libs/go` 0.1.0 → 0.2.0.
- All `staffops_ad_controller_*` / `staffops_ad_worker_*` / `staffops_ad_detection_*` /
  `staffops_ad_alert_*` instruments moved from direct `client_golang` registration to the
  OTel Metrics API (`otel.Meter`, via `otelhelper`), served through
  `otelhelper.MetricsHandler()` on the existing `/metrics` endpoint. Metric names and label
  keys are unchanged. Instruments are created eagerly at package load against the global
  OTel meter, so they're safe to record on before `otelhelper.Setup()` runs (matches how the
  OTel tracing API already behaves) — no separate init call needed, no behavior change for
  components/tests that construct dependencies directly.
- `otelhelper.Setup()` is now called unconditionally in both `main.go` entry points (previously
  skipped entirely when `OTEL_EXPORTER_OTLP_ENDPOINT` was unset). Without a collector configured,
  the library's own Prometheus fallback takes over — same effective behavior as before, now
  going through one code path instead of a hand-rolled `else` branch.
- Removed the app-side `cluster` constant-label wrap (`prometheus.WrapRegistererWith` in both
  `main.go`). Cluster identity is scrape-layer-only now (ServiceMonitor `externalLabels` /
  vmagent), consistent with how every other org-specific label was already handled — see
  [Metrics Reference](docs/site/reference/metrics.md).
- Helm chart: removed `vmServiceScrape` (ServiceMonitor/vm-operator-specific CRD) — vm-operator
  honors the standard `ServiceMonitor` CRD directly, so `serviceMonitor.enabled` is now the
  single scrape toggle for both Prometheus Operator and Prometheus clusters.

## controller — [0.8.0] — 2026-07-14

First homolog deployment (cluster `devops-core`, namespace `staffops`, dry-run) via the
Helm chart, and the first false-positive-reduction lever.

### detection

**Added — per-workload adaptive suppression + observability (2026-07-14)**
- `suppression.exclude_adaptive_workloads_csv`: silences the **adaptive** (EWMA Z-Score)
  detector for named workloads while their static/log signals still fire. Targets
  inherently bursty infra (message brokers, telemetry collectors, service mesh) — measured
  as ~49% of adaptive detections in homolog, the dominant false-positive source. Matches the
  workload the same way the by-workload metric does (`pod` via `ExtractWorkload`, falling
  back to `service_name` for span-metric anomalies with no pod label).
- New `staffops_ad_worker_anomalies_suppressed_total{detector,reason}` counter so the
  suppression effect is directly observable (reasons: `namespace_all`, `namespace_static`,
  `adaptive_workload`).

### detection / measurement

**Added — Synthetic fault-injection harness (P0.1, Phases 1-4) (2026-07-03)**
- New `internal/replay/inject/`: injects `spike`/`ramp`/`step`/`silence` faults into real
  clean series **in memory** (between `QueryRange` and detection), records ground truth,
  and scores detector precision/recall/F1 + recall-by-type + detection latency against it.
  Deterministic by seed. Extends the replay JSON with `injection` + `scoring` blocks.
- `--inject=<profile.yaml>` CLI flag; `--inject=none` runs the FP upper-bound baseline over
  a clean window (all detections scored as FP). Inherits all replay invariants (no
  Redis/AM/gRPC/ML; in-memory only). 98.3% coverage; `code-review` APPROVE-WITH-NITS.
- **Not done**: the gate itself (Phase 5 — real recall/FP numbers) + Phase 6 (feed back into
  Decision 8). Do not mark P0.1 done until numbers exist.

**Fixed — non-finite sample values crash replay JSON (2026-07-03)**
- `SamplesAt` now skips NaN/±Inf points. PromQL ratio rules (`usage / limit`) return `+Inf`
  when a workload has no limit; that flowed into the anomaly `Score` and broke
  `json.Marshal` for **any** replay touching a limitless workload (surfaced by the
  `--inject=none` baseline run). Latent bug, not injection-specific.

**Changed — vendor-neutral Prometheus datasource naming (2026-07-02)**
- Controller config contract `datasources.victoriametrics` → `datasources.prometheus`
  (struct + yaml + `config.yaml`), `grafana_vm_datasource_uid` →
  `grafana_prometheus_datasource_uid`, enrichment `source: "vm"` → `"prometheus"`,
  metric labels `"vm"`/`"vm_range"` → `"prometheus"`/`"prometheus_range"`, replay report
  fields `VMQueries*` → `PromQueries*`, `readiness/vm.go` → `prometheus.go`
  (`VMChecker`→`PromChecker`), `preflightVM`→`preflightProm`. Prometheus is the open
  standard (PromQL); the backend (Prometheus/Thanos/Cortex/Mimir) is an environment choice.
  Left: `ServiceMonitor` (real vm-operator CRD) and the integration-test Prometheus image.
  (Committed 044b213 without a CHANGELOG entry — recorded here retroactively.)

### deps / supply chain

**Changed — otel-helper Go module moved to org (PH.13) (2026-07-02)**
- Dependency moved off the personal account: `github.com/karlipegomes/staffops-otel-libs/go`
  (pseudo-version) → `github.com/staffops/staffops-otel-libs/go` at tagged release **v0.1.0**.
  (Companion commit + tag `go/v0.1.0` pushed to the `StaffOps/staffops-otel-libs` repo.)
- Controller/worker imports + `go.mod` updated; build + full test suite green on Go 1.25.
- The module is now a **public org module**, so all private-module auth was removed:
  Dockerfile (GOPRIVATE + `--mount=type=secret` git-credential dance + `apk add git`),
  CI `test`/`sast`/`build`/`release` (GOPRIVATE env, "Configure git for private module"
  steps, `github_token` build secret), `AGENTS.md`, and `scripts/start.sh` (SSH mount).
- Closes the threat-model "personal-account single-point-of-compromise" finding.

### infra / gitops

**Changed — chart reconciliation + helmfile deploy (PH.16, PH.4) (2026-07-02)**
- **Reconciled the duplicate chart**: the PH.15 chart built under
  `staffops-anomaly-detection/helm-charts/` was in the wrong place — the canonical,
  publishable chart lives in `06-STAFFOPS/helm-charts/charts/anomaly-detection`
  (chart-releaser → `staffops.github.io/helm-charts`). **Removed the duplicate** from
  this repo; the canonical chart is the single source of truth. What the duplicate
  had extra was ported into the canonical chart (below).
- **Ported into the canonical chart**:
  - **PH.4** Redis AUTH — `templates/externalsecret-redis.yaml` (ESO
    `external-secrets.io/v1`, ClusterSecretStore `aws`, sync-wave -1), `redis.auth.*`
    values, helpers `redis.authSecretName`/`authEnabled`; `redis-server --requirepass`,
    `REDIS_PASSWORD` env into controller/worker, `password: ${REDIS_PASSWORD}` in config.
  - **PH.18** `templates/pdb.yaml` (controller `minAvailable:1` guarded to replicaCount>1;
    worker `maxUnavailable:1` — drain-safe at any replica count).
  - **PH.21** `templates/networkpolicy.yaml` (controller/redis/worker/ML ingress rules).
  - **PrometheusRule → PrometheusRule** (`monitoring.coreos.com/v1`) — more portable; vm-operator
    auto-converts. Values key `vmRule` → `prometheusRule`.
- **Deployment via helmfile** (BDC pattern, mirrors `aigent-squad`), NOT ArgoCD
  ApplicationSet: added the `anomaly-detection` release + `anomaly-detection/values.yaml.gotmpl`
  in `02-KUBE/00-CONFIG/k8s-setup/staffops/` targeting the canonical chart, namespace
  `monitoring`, `controller.dryRun=true` for first bring-up. `helmfile template` renders
  the full stack (4 Deployment, ExternalSecret, PVC, 2 PrometheusRule, 4 NetworkPolicy,
  2 PDB, ServiceMonitor, dashboard, RBAC).
- Reviewed by `code-review` (APPROVE-WITH-NITS): added controller NetworkPolicy,
  clarified the ignored worker-PDB value, silenced the redis-cli auth warning.

**Added — Helm chart `helm-charts/anomaly-detection/` (PH.15) (2026-07-02)**
> ⚠️ Superseded by the reconciliation above — this duplicate chart was removed the
> same day; see the canonical chart in `06-STAFFOPS/helm-charts/`.
- Org-neutral: **no corp cost tags** (CostCenter/CostProject/CostScope) baked in —
  those are injected at deploy time by the corp overlay/ApplicationSet. Pod labels
  are `app.kubernetes.io/name`+`version` + runtime `Environment` only.
- ESO wired to the real `core` cluster: `ClusterSecretStore` **`aws`** (verified),
  `external-secrets.io/v1` API, region us-east-1. AWS secret
  `staffops/anomaly-detection/redis-password` created to homologate. Redis PVC 10Gi.
- Full chart (18 files) replacing the raw `controller/deploy/*.yaml`: templates for
  controller, worker, redis (+PVC), ML service, RBAC, ConfigMap, PrometheusRule,
  ServiceMonitor, PDB, NetworkPolicy, ExternalSecret; per-env overlays
  `values-{dev,hml,prd}.yaml`. `helm lint --strict` clean; renders valid YAML in all envs.
- Folds in the pod-template hard-fails as chart-native: **PH.1** (securityContext
  runAsNonRoot/readOnlyRootFilesystem+emptyDir/drop-ALL/runAsUser 65534 on all 4 pods),
  **PH.6** (CostCenter/Environment/name/version labels), **PH.7** (preStop + grace 30s),
  **PH.8** (ML manifest, previously docker-compose only), **PH.14** (datasource URLs
  templated from values, not a hardcoded ConfigMap), **PH.20** (memory-only limits on
  controller/worker), **PH.21** (NetworkPolicy: redis←controller+worker, worker←controller,
  ML←controller), **PH.23** (worker has no events RBAC).
- Redis AUTH (**PH.4**) via ExternalSecret (AWS Secrets Manager) + `password: ${REDIS_PASSWORD}`
  consumed through the Go config's env expansion; sync-wave orders ESO→redis→RBAC→workloads.
- Worker PDB uses `maxUnavailable: 1` and controller PDB is suppressed at replicas=1 —
  avoids node-drain deadlock in DEV.
- Delegated to the `gitops` specialist; independently reviewed by `code-review`
  (caught + fixed a Redis-AUTH config-key mismatch and a single-replica PDB deadlock).
- Not included: ArgoCD ApplicationSet (**PH.16**, next). ML/base images still on
  upstream tags until **PH.3** (golden apko).

### test / ci

**Changed — gofmt across controller + `lint-go` armed (2026-07-02)**
- `gofmt -w` applied to 18 files (comment-alignment / spacing only — no semantic
  change; `go build`/`vet`/`test` all green after). `gofmt -l` now clean.
- CI `lint-go` gate armed (dropped `continue-on-error`): gofmt + go vet now block.

**Changed — Go 1.25 migration + CVE remediation (2026-07-01)**
- Migrated the controller toolchain **Go 1.22 → 1.25** to remediate 11 dependency
  CVEs whose fixes require a newer Go. Cleared (Trivy `go.mod` scan: 0 CRITICAL/HIGH):
  - `google.golang.org/grpc` 1.67.1 → **1.81.1** — CVE-2026-33186 (**CRITICAL**, authz
    bypass via missing leading slash; controller does not use `grpc/authz`, but the
    versioned image must not ship a CRITICAL).
  - `go.opentelemetry.io/otel/*` 1.31 → **1.44** (log family 0.7 → 0.20) — CVE-2026-24051,
    CVE-2026-39883 (PATH-hijack code exec).
  - `golang.org/x/net` 0.30 → **0.55** — 6 CVEs (HTML parse DoS, http2, idna).
  - `golang.org/x/oauth2` 0.22 → **0.36** — CVE-2025-22868 (jws memory exhaustion).
  - Transitive refresh: `protobuf` 1.35 → 1.36.11, `genproto`, `grpc-gateway` 2.22 → 2.29.
- `controller/Dockerfile` builder `golang:1.22-alpine` → `golang:1.25-alpine`;
  CI `go-version` `1.22` → `1.25` (`test.yml` ×2, `sast.yml`).
- Full build + test suite green on Go 1.25; controller coverage preserved (90.4%).
- Fixed the pre-existing `go vet` context leak in `internal/redis/client_test.go`
  (`ctx(t)` registers `cancel` via `t.Cleanup`) — clears a CI rollout-debt item.
- **Gates**: `dep_scan` (Trivy fs) armed (blocking). Image-scan gates
  (`build.yml`/`release.yml`) remain report-only until PH.3 (golden apko base) — the ML
  `python:3.11-slim` debian `perl-base` CVEs are `fix_deferred`/`affected` upstream.

**Added — Go controller coverage to ≥90% + gate armed (PH.9) (2026-06-30)**
- Controller coverage **89.5% → 90.4%** (`./internal/...`).
- `internal/ml/client_error_test.go`: enabled `New`/`Close` (lazy dial), RPC
  error propagation (Health/Forecast/DetectMultivariate), disabled no-op guards
  (`internal/ml` 81% → 94%).
- `internal/baseline/absence_recorder_test.go`: `SetAbsenceRecorder` wiring +
  `noopRecorder` zero-value path (`internal/baseline` → 97%).
- `internal/readiness/ml_test.go`: enabled `MLChecker` branch (Health probe
  against an unreachable endpoint) (`internal/readiness` 91.9% → 96.8%).
- CI `test-go` coverage gate **armed** — hard fail below 90% (was report-only).

**Added — ML service test suite (PH.10) (2026-06-30)**
- ML service coverage **0% → 98.44%** (gate ≥90%). `ml/tests/` was empty.
- `tests/test_forecaster.py`: Prophet mocked (slow + non-deterministic) — asserts
  horizon slicing, breach decision, time-to-breach, confidence clamp, fit frame shape.
- `tests/test_multivariate.py`: Isolation Forest replaced with a controllable fake —
  canonical feature padding, warm-up threshold, periodic refit, contributor selection.
- `tests/test_server.py`: gRPC servicer via a fake `ServicerContext` + injected stubs —
  Forecast/DetectMultivariate happy + error paths (INTERNAL + empty response), Health,
  and `serve()` bootstrap.
- `pytest-cov==5.0.0` added; `--cov=server --cov-fail-under=90` in `pyproject.toml`,
  `server/generated/*` omitted.
- Fixed committed `server/generated/ml_pb2_grpc.py` to a package-relative import
  (`from server.generated import ml_pb2`) — the stub was only importable inside the
  Docker build (via a Dockerfile `sed`), breaking local/CI import. The `sed` is now a no-op.
- CI `test-ml` coverage gate armed (dropped the "empty tests → exit 5" allowance).

### detection

**Added — FDR correction (P0.4) (2026-06-22)**
- Benjamini-Hochberg False Discovery Rate control applied per detection cycle
- Filters adaptive z-score anomalies to cut ~1000+ FP/day from multiple comparisons
- Only adaptive results filtered; static/pattern pass through unchanged
- Config: `controller.fdr_target` (default 0.05 = 5% expected false discoveries)
- Metrics: `staffops_ad_detection_fdr_accepted_total`, `staffops_ad_detection_fdr_rejected_total`
- 20 unit tests, 100% coverage on `internal/detection/fdr.go`

### baseline

**Added — Baseline robustness trio (P2.8, P2.9, P2.10) (2026-06-22)**
- **P2.8 Workload-identity keying**: normalize labels before hashing baseline key — extract
  workload from pod name via regex, drop configurable ephemeral labels. Same workload pods
  now share a baseline across restarts. Config: `baseline.ephemeral_labels`.
- **P2.9 Anti-poisoning gate**: compute z-score BEFORE updating baseline. Skip update when
  z > `poison_threshold` (default 4.0). Prevents slow-ramp attacks from dragging baseline.
  Zero-stddev handling: 1% of EWMA as floor. Metric: `staffops_ad_worker_baseline_poison_rejected_total`.
- **P2.10 Absence-of-signal detection**: new `internal/absence/` package. Tracks series liveness
  via `AbsenceRecorder` interface on every baseline evaluation. Background checker fires alerts
  when previously-active series go silent for > threshold (default 5m). Suppresses known-idle
  namespaces via pattern config. Startup grace period prevents false alerts on controller restart.
  11 unit tests.

### docs / specs

**Added — Full spec coverage (2026-06-22)**
- 21 specs covering every significant ROADMAP item (retroactive for completed + new for planned)
- `specs/README.md` index with status tracking and project direction summary
- MkDocs restructured to `docs/site/` pattern (aligned with staffops-aigent-squad)
- All specs vendor-agnostic ("Prometheus-compatible TSDB" not Prometheus)

### ci / build

**Added — GitHub Actions CI + hardened images (2026-06-21)**
- GitHub Actions (`test`/`sast`/`build`/`release`/`docs`): build/push SHA-tagged images
  to **Docker Hub** (repo is private; org pattern, not ghcr), with Trivy + CycloneDX SBOM
  and gosec/bandit SAST. Private-module auth via `DOCS_DEPLOY_TOKEN`. Security/lint and
  coverage gates are report-only during rollout (PH.2/PH.9/PH.12).
- ML image is now multi-stage — runtime layer drops `gcc/g++` and `grpcio-tools` (PH.5).
- All images run as nonroot `USER 65534`; controller/worker add `tzdata` (PH.1, image side).
- Runtime `grpcio` bumped to 1.65.4, past CVE-2024-7246 (PH.24).
- Private module `staffops-otel-libs` is fetched via BuildKit SSH forwarding
  (`--mount=type=ssh` / `ssh: default`) — deploy key never enters an image layer.
- `replay/window_test.go` fix verified; full Go suite green (PH.11).

### repo

**Changed — AI-tool-agnostic layout (2026-06-21)**
- `AGENTS.md` is now the canonical, tool-neutral agent guide; `CLAUDE.md` is a
  one-line pointer (`See @AGENTS.md`).
- Specs moved from `.kiro/specs/` to `specs/` (history preserved); `.kiro/` removed.
- `.gitignore` excludes local AI-tool dirs and Go coverage artifacts.

### docs

**Added — Multi-specialist evaluation (2026-06-16)**
- Independent review by `dev`, `security`, `gitops`, `anomaly-detection` subagents in parallel.
- ROADMAP `Phase 5 Pre-Reqs (Production Hardening)` section added with 25 tracked items (PH.1–PH.25) covering: Kyverno admission hard-fails (no securityContext, `:latest` tag, non-golden bases, Redis no auth, ML compiler in prod image, missing labels, no preStop, no ML manifest), test & CI gates (Go 35 % → ≥ 90 %, ML 0 % → ≥ 90 %, failing test, missing CI), org-neutrality completion (`karlipegomes/staffops-otel-libs` rename, BDC URLs out of in-repo ConfigMap), Helm + ArgoCD migration, NetworkPolicy + IRSA + worker RBAC trim, dependency hygiene (`grpcio` CVE-2024-7246).
- New spec `specs/production-hardening/` (requirements + tasks; no `design.md` — pure template work).
- `docs/threat-model-and-limitations.md` extended with three concerns surfaced by independent security review (supply chain of the detector itself, Redis state-integrity tampering, gRPC plaintext between components) and refinements to P2.8/P2.9/P2.10 scope.

### controller

**Added — OTel SDK integration (P4.A.5)**
- Integrated `github.com/karlipegomes/staffops-otel-libs/go` — traces + logs via OTLP
- gRPC interceptors on controller→worker calls (distributed tracing)
- Graceful fallback: when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty, uses plain JSON slog (no crash)

**Added — Dashboard refresh (P4.A.4)**
- `controller/deploy/dashboard.json` rewritten: 18 panels, all `staffops_ad_*` taxonomy
- Alerts Fired uses Prometheus rate (was Loki count_over_time)
- Workers Available stat panel, Cardinality Watch table

**Changed — Detection rules overhaul**
- Removed static CPU/memory ratio rules (duplicated VMAlert, caused 82% noise)
- Added: istio 5xx rate, kestrel queued requests, .NET threadpool saturation, http client queue time, otel collector dropping spans, rollout stuck
- Added adaptive: istio error rate by workload, .NET GC pause/heap growth, http_client active requests, queue depth/fails, hikari/go_sql pool saturation, otel collector queue, karpenter scheduling
- Exclude namespaces expanded: +monitoring, istio-system, scaleops-system

**Changed — Performance tuning**
- `job_interval`: 30s → 60s (reduce Prometheus load)
- Prometheus rate limiter: 100/s → 20/s per worker
- Enrichment concurrency: 5 → 2
- gRPC call timeout: 30s → 90s (handles slow Prometheus)
- Prometheus query timeout: 30s → 60s

**⚠️ PENDING VALIDATION** (blocked by Prometheus/Loki degradation 2026-06-14 evening):
- [ ] Replay with new rules against stable window (confirm spanmetrics/istio/.NET signals produce detections)
- [ ] Live observation for ≥1h with healthy backends (confirm alert quality, dedup, workload correlation)
- [ ] Verify enrichment bundles work with new rule labels (service_name vs namespace/pod)

**Fixed**
- `correlator.go`: `workloadKey()` now falls back to `service_name` when the pod label is empty. Service-level anomalies (latency/error-rate by service) no longer collapse into a single `/` group (was 348 anomalies in one bucket).

**Added — Replay mode (P3.1, 12/16 tasks)**
- `internal/replay/`: offline detection over historical metrics/logs with zero side effects (no Redis/Alertmanager/gRPC/ML).
  - Window parser (durations, `Nd` days, RFC3339, UTC), in-memory baseline store (Welford+EWMA), `baseline.Evaluator` interface
  - Tick simulator: 1h chunked range queries, dynamic warmup split, graceful per-tick query-error skip, SIGINT partial flush
  - Report serializers: JSON (full schema, `schema_version: 1`) + Markdown (tables + ASCII sparklines), both UTC
  - CLI: `--replay --from --to --output --warmup-fraction --max-range --max-anomalies` with Prometheus/Loki/output pre-flight checks (ML is V2)
  - In-memory execution metrics embedded in `metadata.execution_metrics` (no Prometheus exposure in V1)
- Remaining: T13 integration test, T14 smoke test, T15 README, T16 ROADMAP move-to-Done.

### ml

**Fixed**
- `multivariate.py`: introduce `CANONICAL_FEATURES` (10 fixed features) with `_normalize()` padding. Eliminates the `ValueError: X has N features but model expects M` when pod-level (6+) and service-level (3-5) anomalies hit the same IsolationForest (was ~33% error rate).

---

## controller — [0.7.0] — 2026-05-30

Consolidated milestone covering a full day of MVP iteration. Multiple sub-features were implemented and validated together; they ship as a single release because none of them justify an independent bump on its own (still pre-production, dry-run only, no cluster deploy).

### Added

**Metrics taxonomy & build introspection**
- All Prometheus metrics renamed under `staffops_ad_*` with 5 sub-namespaces:
  - `staffops_ad_controller_*` — orchestration, leader, dispatch, cycles
  - `staffops_ad_worker_*` — query execution, baselines, Redis ops
  - `staffops_ad_detection_*` — anomalies detected (cross-cutting)
  - `staffops_ad_alert_*` — alert pipeline
  - `staffops_ad_ml_*` — ML service ops (Python side)
- `internal/version/version.go` SSOT and `staffops_ad_controller_build_info{version}` metric

**P1.1 — Label-based pivot (anomaly enrichment)**
- `internal/enrichment/` — identity extraction (pod/service kinds), template substitution (`$pod`, `$namespace`, `$service_name`, etc.), bounded concurrency, Redis-backed cache, multi-source (Prometheus + Loki)
- Per-kind metrics: `staffops_ad_controller_enrichment_runs_total`, `_duration_seconds`, `_cache_hits_total`, `_cache_misses_total`, `_query_errors_total`
- Default `pod_bundle` (cpu_ratio, memory_ratio, restarts_5m, oom_kills, ready_replicas, error_logs_1m) and `service_bundle` (error_rate_1m, request_rate_1m, latency_p99_5m)
- Alert payload now ships with `enrich_*` annotations and a one-line `context` summary

**P1.2 — Alert payload with deep links**
- `internal/alert/links.go` `LinkBuilder` rendering Grafana Explore, Tempo TraceQL, Loki LogQL, and Runbook URLs into Alertmanager annotations
- New annotations: `grafana_url`, `tempo_url`, `loki_url`, `runbook_url`
- ±15min framing for metrics/traces, ±5min for logs, anchored at anomaly timestamp
- Per-detector runbook paths (`<base>/<detector>`)

**P1.3 — Complete readiness checks**
- `internal/readiness/` with checkers for Prometheus, Loki, Alertmanager, ML
- `/readyz` now probes all upstream dependencies (was Redis-only before)
- All probes capped at 3s
- ML probe is no-op when `ml.enabled=false`
- New metric `staffops_ad_controller_readiness_checks_total{dependency,result}`
- ML client gained `Health(ctx) error` and `Enabled() bool` methods

**P2.1 — ML multivariate proper feature vector**
- `internal/ml/features.go` `BuildFeatureVector(anomaly, enrichment.Bundle)` produces stable, named feature vectors (anomaly_score, anomaly_value, plus enrichment results)
- ML now runs **once per correlated alert** (post-enrichment), with 5-8 distinct features
- New `correlation.MLDetection` field on `CorrelatedAlert` carrying `IsAnomaly`, `Score`, `Contributors`, `FeatureCount`
- Auto-severity escalation: `warning` → `critical` when ML confirms
- New annotations: `ml_score`, `ml_features`, `ml_contributors`; new label `ml_confirmed=true`

**12-factor compliance — env-var-driven config**
- All endpoint URLs and environment-specific values driven by env vars
- `${VAR}` (required, fail-fast) and `${VAR:default}` (optional fallback) substitution in `config.yaml`
- Config loader expands placeholders BEFORE YAML parse, comment-aware (skips `#` lines)
- `.env.example` at repo root documenting every supported variable
- `docker-compose.yaml` passes env vars via YAML anchors (`x-app-env`)
- `.env` files gitignored

### Changed

- `correlation.NewCorrelator` signature now accepts an `Enricher` interface
- `alert.NewDispatcher` signature now accepts a `*LinkBuilder`
- `cmd/controller/main.go` flush loop uses `FireCorrelated` and runs ML per-alert
- Removed broken pre-flush `mlClient.DetectMultivariate(ctx, samples)` call (collapsed same-metric anomalies into single map key, defeating Isolation Forest)
- Removed default URLs from `setDefaults()` in `internal/config/config.go`

### Migration notes

- Operators must populate `.env` (or set env vars) before starting the stack. Required: `PROMETHEUS_URL`, `LOKI_URL`, `ALERTMANAGER_URL`. Stack fails fast if missing.
- Dashboards/alerts referencing the old `anomaly_*` metric names must update to `staffops_ad_*` taxonomy.
- ML invocation pattern changed (per-alert with feature vector, vs per-cycle with raw values). Operators may see fewer total ML calls but each is meaningful.

### Tests

- 26 new unit tests across `internal/alert/links_test.go`, `internal/readiness/readiness_test.go`, `internal/config/expand_test.go`, `internal/ml/features_test.go`

### Not in this release (still on roadmap)

- Cluster deploy / removal of `--dry-run`
- K8s Lease leader election
- Prophet Forecast wiring (P2.2)
- Replay mode / feedback loop / SLO-aware severity

---

## controller — [0.6.0] — 2026-05-26

### Added
- Initial controller + workers + Redis-backed baselines
- Static, Adaptive (Z-Score), and Pattern detectors
- Correlation engine with Redis-backed dedup
- Alertmanager dispatcher (dry-run mode)
- Suppression filter (namespace-level + static-only)
- Config hot-reload watcher
- docker-compose stack
- Initial ML gRPC client integration (later refactored in 0.7.0)

---

## ml — [0.2.0] — 2026-05-30

### Added
- Custom Prometheus metrics under `staffops_ad_ml_*`:
  - `staffops_ad_ml_requests_total{method,status}`
  - `staffops_ad_ml_request_duration_seconds{method}`
  - `staffops_ad_ml_forecast_breach_predicted_total`
  - `staffops_ad_ml_forecast_input_size`, `_confidence`
  - `staffops_ad_ml_multivariate_anomalies_total`
  - `staffops_ad_ml_multivariate_input_size`, `_score`
  - `staffops_ad_ml_ready`
  - `staffops_ad_ml_model_version_info{version}`
- gRPC error handling — sets `INTERNAL` status code on exceptions

### Changed
- Removed legacy metrics `ml_requests_total` and `ml_request_duration_seconds` (replaced by `staffops_ad_ml_*`)

---

## ml — [0.1.0] — 2026-05-26

### Added
- gRPC server on port 50051 with `Forecast` (Prophet) and `DetectMultivariate` (Isolation Forest)
- Health endpoint
