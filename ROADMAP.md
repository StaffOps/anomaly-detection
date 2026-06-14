# Roadmap

Planned improvements for `staffops-anomaly-detection`. Ordered by priority — items higher up are nearer-term.

---

## Phase 1 — Detection quality & UX

### ✅ ~~P1.1 — Label-based pivot (anomaly enrichment)~~ — **DONE in controller 0.7.0**

Implemented `internal/enrichment/` with identity extraction, template substitution, bounded concurrency, Redis-backed cache, and alert payload enrichment via `FireCorrelated`. Default bundles for pod and service kinds shipped in `config.yaml`. Validated in real production traffic — alerts now carry `cpu_ratio`, `memory_ratio`, `restarts_5m`, `error_rate_1m`, `latency_p99_5m`, etc. as context.

### ✅ ~~P1.2 — Alert payload with links~~ — **DONE in controller 0.7.0**

`LinkBuilder` renders Grafana Explore, Tempo TraceQL, Loki LogQL, and per-detector Runbook URLs into alert annotations. URL framing is anchored at the anomaly timestamp (±15min metrics/traces, ±5min logs) and uses the most specific labels available. 6 unit tests.

### ✅ ~~P1.3 — Complete readiness checks~~ — **DONE in controller 0.7.0**

`/readyz` now probes Redis (existing) plus VictoriaMetrics, Loki, Alertmanager, and ML service via `internal/readiness/`. All probes capped at 3s. ML probe is no-op when disabled. New metric `staffops_ad_controller_readiness_checks_total{dependency,result}`. 7 unit tests with `httptest`.

---

## Phase 2 — ML maturity

### ✅ ~~P2.1 — Fix ML multivariate (proper feature vector)~~ — **DONE in controller 0.7.0**

`internal/ml/features.go` builds stable, named feature vectors combining the triggering anomaly with enrichment bundle results. ML now runs **per correlated alert** (post-enrichment) with 5-8 distinct features (cpu_ratio, memory_ratio, restarts, error_rate, latency, etc.) — not the broken same-metric-collision pattern. Auto-escalates `warning` → `critical` on ML confirm. New annotations `ml_score`, `ml_features`, `ml_contributors`.

### ✅ ~~P2.4 — Workload-aware correlation (sibling check)~~ — **DONE in controller 0.7.0** (awaiting prod validation)

`internal/correlation/workload.go` extracts workload from pod name via regex (Deployment `<name>-<rs_hash>-<pod_hash>`, StatefulSet `<name>-<N>`, DaemonSet `<name>-<5-char>`). Correlator detects workload patterns first: when ≥3 sibling pods of the same workload anomaly within the correlation window, emits 1 workload-level alert and suppresses contributing pod groups. 15 unit tests. Configurable via `controller.workload_pattern_min_pods` (default 3). Metrics: `workload_patterns_total`, `pod_alerts_suppressed_total`.

**Awaiting prod validation**: workload patterns require ≥3 simultaneous sibling spikes — has not occurred yet in healthy cluster. Continue observing 24-48h. Once `workload_patterns_total > 0` consistently observed → milestone candidate for `0.8.0` bump per `version-management.md` steering.

### 🎯 P2.5 — ML feature: `replica_anomaly_fraction`

After P2.4 is prod-validated, feed `replica_anomaly_fraction` (0-1) into the Isolation Forest feature vector. ML naturally learns "high fraction = workload pattern" without hardcoded rule. Synergy with P2.4: B reduces noise via rule, C teaches ML to weight it.

**Status**: blocked on P2.4 prod validation.

### 🎯 P2.6 — Cross-workload dependency mapping (futuro, escopo grande)

Mapear como anomalias em um workload se propagam pra outros — request flows, batch periods, dependências de tempo. Permite ao operador ver "anomalia em service A → afeta service B → afeta service C" como uma cadeia, em vez de 3 alertas isolados.

**Sinais possíveis**:
- **Spans (Tempo)**: dependency map já existe via `service_graph` — extrair grafo e correlacionar com anomalias
- **Cross-time-series correlation**: análise estatística de quão correlacionadas duas séries são em janelas históricas (Pearson, dynamic time warping)
- **Schedule-aware**: cron jobs com horários conhecidos → spike em DB minutos depois → relacionar
- **Request flows**: se workload A chama B e A tem latência alta, B provavelmente tem causa
- **Batch periods**: aprendizado de sazonalidade (mês/semana) parcialmente coberto pelo Prophet (P2.2)

**Saída esperada**: alerta enriquecido com "linhagem" — "este alerta em service B provavelmente é consequência de A (correlation 0.92, dependency confirmed via Tempo)".

**Por que escopo grande**: requer Tempo integration, possível storage de correlation matrices, modelos de causalidade. Fase 3+ provavelmente. Candidato a spec próprio em `.kiro/specs/cross-workload-dependency-mapping/`.

### 🎯 P2.2 — Wire ML Forecast (Prophet)

Client method `Forecast` exists but isn't called. Needs:
- Baseline store to expose **time-series history** (currently only stats — mean, stddev, EWMA)
- Periodic forecast job (e.g., once per hour per series with sufficient history)
- Wire forecast results into correlator with `detector="ml_forecast"`
- Cache forecasts in Redis (Prophet is ~500ms-2s)

**Effort**: medium. Requires baseline store schema extension (sliding window of values + timestamps).

### 🎯 P2.3 — Multivariate per-namespace mode

Detect if **multiple services in same namespace** are simultaneously anomalous → indicates shared-dependency issue (DB, cache, network).

Aggregate anomalies by namespace, run multivariate on `{count_anomalous_pods, distinct_services_affected, aggregate_severity}`.

---

## Phase 3 — Operational maturity

### ✅ ~~P3.1 — Replay mode~~ — **DONE**

Spec at [`.kiro/specs/replay-mode/`](.kiro/specs/replay-mode/). All 16 tasks complete.

CLI: `controller --replay --from=24h --to=1h --config=cand.yaml --output=report.json` simulates detection over historical metrics/logs without side effects (no Redis writes, no Alertmanager dispatches, no gRPC fan-out).

**Smoke test result** (2026-05-31): Ran against production endpoints (VM bdc.app.br, Loki bdc.app.br). Pre-flight OK, engine processed ticks correctly, graceful partial flush on SIGINT. JSON schema valid, Markdown readable. VM queries p95 ~1s. Zero side effects confirmed.

**V1 excludes**: ground-truth comparison (TPs/FPs/FNs), ML wired, query cache, distributed replay. All scoped as V2.

### ✅ ~~P3.2 — Top noisy workloads dashboard / VMRule~~ — **DONE**

New metric `staffops_ad_detection_anomalies_by_workload_total{namespace, workload, severity}` (bounded labels: workload extracted via `correlation.ExtractWorkload`, falls back to `service_name`, empty values normalized to `unknown`).

Recording rules in `controller/deploy/vmrules.yaml`:
- `staffops:detection_anomalies_24h:by_workload` — 24h aggregate for top-N panels
- `staffops:detection_anomalies_24h:by_workload_severity` — sliced by severity
- `staffops:detection_anomalies_1h:by_workload` — short window for "currently noisy"

Operator dashboard panels added (`scripts/observability/grafana/dashboards/operator.json`):
- 🔥 Top noisy workloads (24h) — table with topk(20), color-graded
- 📊 Cardinality watch — monitors series count per `staffops_ad_*` metric with thresholds at 500/1000/2000 (steering hard limit)

VMRule alert `StaffOpsADWorkloadChronicallyNoisy` fires (info severity) when a workload exceeds 100 anomalies in 24h — operator hint to add suppression or investigate.

Cardinality justified: severity(3) × namespace(~50) × workload(~30/ns avg) ≈ 4500 series — bounded growth via deployment count, not pod count.

### 🎯 P3.3 — Feedback loop

Slack reaction-based feedback on alerts:
- ✅ True positive
- ❌ False positive

Stored in Redis with TTL 30d. Used to:
- Compute precision/recall per rule
- Auto-tune `zscore_threshold` per metric
- Surface "your top 5 noisy rules" weekly

**Effort**: large. Requires Slack interaction handler + feedback store. Candidato a spec próprio em `.kiro/specs/feedback-loop/`.

### 🎯 P3.4 — SLO-aware severity

Load SLO state (from VMRules or config). Adjust severity dynamically:
- SLO budget > 80% → downgrade warning to info (less noise)
- SLO budget < 20% → upgrade warning to critical (max attention)

**Effort**: medium. Needs SLO catalog integration.

---

## Phase 4 — Observability hardening (PREREQUISITE para deploy)

Established 2026-05-30 after observability advisor review (drawing on `vm-cardinality-management`, `multicluster-label-strategy`, `grafana-cross-signal-correlation` skills). All items below are **prerequisites for any cluster deploy** (Phase 5) — without them we either ship broken instrumentation or a cardinality bomb.

### ✅ ~~P4.A.1 — Fix instrumentation bugs~~ — **DONE in controller 0.7.0**

All three bugs documented in `ALARMs.md` were fixed during the 0.7.0 implementation:

- `alerts_fired_total`: Counter incremented BEFORE `if d.dryRun` in `dispatcher.go` — measures intent, not delivery.
- `workers_available`: Gauge set on every tick in `main.go` via `workerConn.GetState()` (connectivity.Ready/Idle → 1, else → 0).
- `cycle_duration_seconds`: Custom buckets `[1, 2.5, 5, 10, 20, 30, 60]` defined in `metrics.go`.

### ✅ ~~P4.A.2 — Cardinality cleanup~~ — **DONE in controller 0.7.0**

No `identity` label exists on any counter/histogram. `AlertsFired` uses only `[severity]`. Full pod identity goes to Alertmanager **annotations** (not indexed labels) and structured logs. The `workload` label in AM alerts uses bounded extraction via `correlation.ExtractWorkload()`.

### ✅ ~~P4.A.3 — Multi-cluster constant labels~~ — **DONE in controller 0.7.0**

`main.go` wraps the registry with `prometheus.WrapRegistererWith(constLabels{cluster: cfg.Cluster}, ControllerRegistry)`. The `eks_cluster` label was deliberately excluded from app code — per `observability-principles.md` steering, environment-specific labels belong at the scrape layer (vmagent `externalLabels` in prod, `static_configs.labels` in local dev). Documented in `controller/README.md` "Multi-cluster labels" section.

### ✅ ~~P4.A.4 — Dashboard refresh pós P4.A.1+P4.A.2~~ — **DONE**

`controller/deploy/dashboard.json` rewritten (18 panels, uid `staffops-ad-system-health`):

- ✅ All queries use `staffops_ad_*` taxonomy (zero old `anomaly_*` references)
- ✅ Alerts Fired/min: `sum(rate(staffops_ad_alert_fired_total[5m])) * 60` (Prometheus, replaces Loki `count_over_time`)
- ✅ Workers Available: stat panel with value mapping (1=UP/green, 0=DOWN/red)
- ✅ Cardinality Watch: `topk(15, count by (__name__) ({__name__=~"staffops_ad_.+"}))` — table with color thresholds at 500/1000/2000
- ✅ Recent Alerts (Loki): preserved as ground truth (`{service="controller"} | json | msg="alert_fired"`)
- ✅ Full system health: cycles, anomalies by signal/severity, cycle/job/query duration histograms, baselines, Redis ops, K8s events, detector breakdown

### ✅ ~~P4.A.5 — OTel SDK adoption~~ — **DONE**

Integrated `github.com/staffops/otel-helper-go` (corporate OTel lib):

- Controller + Worker: `otelhelper.Setup()` with traces + logs (metrics disabled — Prometheus client_golang used directly)
- Controller: `UnaryClientInterceptor` + `StreamClientInterceptor` on gRPC dial to workers
- Worker: `UnaryServerInterceptor` + `StreamServerInterceptor` on gRPC server
- Logger: `otelhelper.NewLogger()` bridges slog → OTel logs (trace_id/span_id auto-injected)
- Graceful fallback: when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty/unreachable, falls back to plain JSON slog (no crash)
- Health check RPCs filtered from traces by interceptors

When an OTel Collector is deployed alongside (P5.2), logs gain `traceID` enabling Loki `derivedFields` → Tempo trace navigation in Grafana.

---

## Phase 5 — Cluster readiness (was Phase 4)

Pré-requisito: ~~P4.A.1 a P4.A.4~~ ✅ all done. P4.A.5 (OTel SDK) deferred to P6, não bloqueia.

### ✅ ~~P5.1 — K8s Lease leader election~~ — **DONE**

`internal/leader/` package wraps `k8s.io/client-go/tools/leaderelection`. Configurable via `controller.leader_election.enabled` in config.yaml (default false for local dev). When enabled, only the lease holder runs detection cycles; followers stay warm and take over within ~17s on lease loss (LeaseDuration 15s + RetryPeriod 2s).

**Identity** defaults to `POD_NAME` (downward API), falls back to hostname. Updates `staffops_ad_controller_is_leader` gauge and `staffops_ad_controller_leader_transitions_total` counter automatically.

**RBAC**: Role grants `coordination.k8s.io/leases` get/create/update in the controller's namespace (already in `controller/deploy/controller.yaml`).

**Tests**: 7 unit tests covering validation errors, identity resolution, kubeconfig handling. Cluster integration validation in P5.2.

### 🎯 P5.2 — Deploy to cluster (was P4.2)

`deploy/` has manifests but never validated in cluster. Needed:
- Test in dev cluster
- Validate IRSA for AWS Secrets Manager (alert webhook secrets)
- ApplicationSet for ArgoCD multi-cluster
- Validate cosign signing in CI

**Effort**: medium.

### 🎯 P5.3 — Remove `--dry-run` and validate real alerts (was P4.3)

End-to-end test: anomaly → Alertmanager → Slack channel. Currently always dry-run.

**Pre-req**: P3.2 (visibility into rule quality) + P3.3 (feedback) — to avoid alert flood.

### 🎯 P5.4 — Cardinality guard (was P4.4)

Self-protection: if `staffops_ad_worker_baseline_series_tracked` > N (configurable, default 10k), workers stop creating new baselines and emit alert. Prevents Redis OOM if label cardinality explodes.

**Effort**: small.

### 🎯 P5.5 — Agent API Integration (staffops-chaitops)

Invoke staffops-chaitops Agent API on high-confidence anomalies to trigger automated squad investigation. Fire-and-forget with circuit breaker, bounded concurrency (max 5), deduplication.

**Trigger conditions**: severity ≥ warning AND (ml_score ≥ 0.7 OR correlation_group_size ≥ 3)

**Pre-req**: P5.3 (controller out of dry-run, real alerts flowing) + P2.4 prod-validated (workload patterns confirmed).

**Key components**:
- `internal/agentapi/` — HTTP client with circuit breaker (3-state: closed/open/half-open)
- `internal/agentapi/dedup.go` — Redis-backed dedup (same correlation group → 1 squad, not N)
- Bounded concurrency: semaphore max 5 concurrent calls
- Fire-and-forget: does NOT wait for squad result; controller continues detection cycle
- Graceful degradation: if Agent API unavailable, normal Alertmanager flow continues

**Spec**: `.kiro/specs/agent-api-integration/`

**Effort**: 22 tasks (6 core + 6 integration/observability + 7 tests + 3 docs)

**Status**: spec complete (2026-06-02), implementation blocked on P5.3.

---

## Phase 6 — Observability of the observability (was Phase 5)

### 🎯 P6.1 — Self-monitoring VMRules (was P5.1)

VMRules covering `staffops_ad_*` metrics:
- Cycle duration p99 > 30s → alert
- Worker query error rate > 10% → alert
- ML calls error rate > 5% → alert
- ReadinessChecks failing → alert
- Detection cycle gap > 90s (controller stuck) → critical

**Effort**: small.

### 🎯 P6.2 — Grafana dashboard (was P5.2)

Already have `deploy/dashboard.json` but needs update for new `staffops_ad_*` taxonomy. Include:
- Detection volume (per detector, per signal)
- Top noisy workloads
- ML call rate / latency / breach predictions
- Alert dedup rate
- Worker fleet health

**Effort**: small (refactor existing dashboard).

### ✅ ~~P6.3 — OTel SDK adoption~~ — **DONE (via P4.A.5)**

Completed as P4.A.5 using `github.com/staffops/otel-helper-go`. Traces + logs enabled via OTLP, gRPC interceptors provide distributed tracing across controller ↔ worker communication.

---

## Done ✅

### controller 0.7.0 (2026-05-30) — MVP enriquecido + ML correto + workload-aware
Single consolidated milestone covering a day of iteration. See `CHANGELOG.md` for the full list. Highlights:
- `staffops_ad_*` metric taxonomy (5 sub-namespaces) + `build_info`
- **P1.1** label-based pivot / enrichment (`internal/enrichment/`)
- **P1.2** alert deep links (Grafana / Tempo / Loki / Runbook)
- **P1.3** complete `/readyz` probes (VM / Loki / Alertmanager / ML)
- **P2.1** ML multivariate proper feature vector (fixes same-metric collision bug)
- **P2.4** workload-aware correlation (`internal/correlation/workload.go`, awaiting prod validation)
- 12-factor: all endpoint URLs externalized to env vars
- Module path renamed `bigdatacorp` → `staffops`
- Operator dashboard simplified to single primary view (4 cluttered ones removed)
- 26+ new unit tests

### controller 0.6.0 (2026-05-26)
- Initial controller + workers + Redis baselines
- Static / Adaptive / Pattern detectors
- Correlation engine with Redis dedup
- Alertmanager dispatcher (dry-run)
- Suppression filter, config hot-reload
- docker-compose stack
- Initial ML client (later refactored in 0.7.0)

### ml 0.2.0 (2026-05-30)
- `staffops_ad_ml_*` custom Prometheus metrics
- gRPC error handling

### ml 0.1.0 (2026-05-26)
- gRPC server (Forecast + DetectMultivariate + Health)

---

## Anti-goals (out of scope)

- Replacing VMAlert / Prometheus alerting — this is **complementary**, not replacement
- Built-in incident management — ship signal to existing tools (Alertmanager → Slack/PagerDuty)
- Multi-cluster federation in this repo — handled by collector layer
- UI/web dashboard — Grafana is the UI

---

## Decision log

- **2026-05-30**: Decided to keep monorepo (controller + ml together) over separate repos. Easier dev loop, single docker-compose.
- **2026-05-30**: Adopted `staffops_ad_*` metric prefix with 5 sub-namespaces (controller/worker/detection/alert/ml) over single flat namespace.
- **2026-05-30**: Versions bumped manually per `version-management.md` steering. Each component (controller, ml) versioned independently.
- **2026-05-30**: Module path renamed `github.com/bigdatacorp/staffops-anomaly-detection` → `github.com/staffops/staffops-anomaly-detection` for org-neutrality. All Go imports + Python proto descriptors regenerated.
- **2026-05-30**: All endpoint URLs / org-specific identifiers moved to env vars (`${VAR}` / `${VAR:default}` substitution in `config.yaml`). Required vars: `VM_URL`, `LOKI_URL`, `ALERTMANAGER_URL`. Compose fails fast.
- **2026-05-30**: Adopted spec-driven workflow per `staffops_agent_definition/steering/spec-driven-workflow.md`. `.kiro/specs/replay-mode/` is the pilot — design reviewed before implementation, 11 ambiguities decided up front.
- **2026-05-30**: Observability hardening promoted to dedicated phase (Phase 4). Three blockers identified (instrumentation bugs, `identity` label cardinality bomb, missing multi-cluster labels) that must be fixed before any cluster deploy. Renumbered: old Phase 4 → new Phase 5, old Phase 5 → new Phase 6.
- **2026-05-30**: Subagent tool (Kiro CLI parallel execution) verified non-functional in this environment (3 consecutive `No result` returns including minimal `summary`-only ping). Falling back to serial implementation by main agent. Will retry when environment changes.
- **2026-05-30**: `eks_cluster` BDC-specific label removed from app code (was added briefly during P4.A.3, then reverted). Per `observability-principles.md` steering: app emits only `service.name` (here, `cluster`); environment-specific labels (`eks_cluster`, `environment`, `team`, `region`) belong at the scrape layer. Implemented via `static_configs.labels` per scrape job in `scripts/observability/prometheus.yml` for local dev; production uses `vmagent externalLabels`. Documented in `controller/README.md` "Multi-cluster labels" section. App stays org-agnostic — same as the `bigdatacorp` rename earlier.
- **2026-05-30**: Created `code-review` subagent — rigorous quality-gate persona that reviews diffs against 7 gates (correctness, steering, idiomatic, readability, tests, performance, security). Does not implement; only reviews. Total subagent count now 10. Subagent tool spawning still broken in this env, but main agent can adopt the persona by reading the prompt directly.
