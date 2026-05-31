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

### 🚧 P3.1 — Replay mode — **IN PROGRESS (5/16 tasks done)**

Spec at [`.kiro/specs/replay-mode/`](.kiro/specs/replay-mode/) covering requirements, design, tasks. **Pilot of spec-driven workflow** per `staffops_agent_definition/steering/spec-driven-workflow.md`.

CLI: `controller --replay --from=24h --to=1h --config=cand.yaml --output=report.json` simulates detection over historical metrics/logs without side effects (no Redis writes, no Alertmanager dispatches, no gRPC fan-out).

**Done so far** (controller branch, not yet merged):
- T1 — Window parser (`internal/replay/window.go`, 22 sub-tests passing)
- T2 — VM range query (`MetricsPoller.QueryRange` + `TimeSeries`/`Point` types)
- T3 — Loki range query (`LogsPoller.QueryMetricRange`)
- T4 — InMemStore baseline (mirrors Welford+EWMA, 7 unit tests)
- T5 — `baseline.Evaluator` interface (Store and InMemStore both satisfy)

**Remaining** (T6–T16): ReplayConfig parsing, tick simulator with error handling + `IsWarmingUp` filter, range-to-instant adapter, Report struct + JSON/MD serializers, CLI flags + dispatch, replay-specific in-memory metrics (no Prom exposure V1), integration test, smoke test, README + ROADMAP updates.

**V1 explicitly excludes**: ground-truth comparison (TPs/FPs/FNs), ML wired, query cache for fast iteration, distributed replay. All scoped as V2.

**Effort remaining**: ~5 days sequential.

### 🎯 P3.2 — Top noisy workloads dashboard / VMRule

Recording rule + Grafana panel showing top-N workloads by anomaly count over 24h. Operator uses to:
- Tune suppression rules
- Identify chronically broken workloads
- Catch detection drift

```promql
topk(20, sum by (namespace, workload) (
  increase(staffops_ad_detection_anomalies_total[24h])
))
```

**Effort**: small (recording rule + dashboard panel). Depends on P4.A.2 cardinality cleanup (use `workload` label, not `pod`).

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

### 🎯 P4.A.1 — Fix instrumentation bugs

Bugs documented in `ALARMs.md` causing dashboards to lie:

| Bug | Fix | Risk |
|-----|-----|------|
| `alerts_fired_total=0` em dry-run | Move `metrics.AlertsFiredTotal.Inc()` to BEFORE `if !d.dryRun` block in `dispatcher.go`. Counter mede intent, não delivery. | low |
| `workers_available=0` always | Gauge nunca é setado — wire em `runCycle` no controller, refletir health real dos workers via gRPC ping | low |
| `cycle_duration_seconds` clip 10s | Adicionar `Buckets: []float64{1, 2.5, 5, 10, 20, 30, 60}` na registration do histogram | low |

**Effort**: ~30min. Test via docker-compose: counters incrementam, gauge reflete fleet, histogram p99 não clipa.

### 🎯 P4.A.2 — Cardinality cleanup (`identity` label removal)

⚠️ **Steering-critical** per `vm-cardinality-management`: nunca usar labels unbounded. Hoje:

- `staffops_ad_alert_alerts_fired_total{namespace, identity, severity, kind, ...}` — `identity` é pod name em alertas pod-level → unbounded
- Em prod com 1000 pods × restarts ao longo de meses → 100k+ séries só desse contador
- Steering hard-limit: 2000 séries por metric

**Solução**:
- Remover `identity` de todos counters/histograms em `internal/metrics/`
- Substituir por `workload` (extraído via `correlation.ExtractWorkload`, bounded ≈ deployment count)
- Manter `identity` em **logs** (Loki) — alta cardinalidade aceitável lá
- Manter `kind` (pod vs workload) — bounded, 2 valores

**Effort**: ~1h. Audit todos os points de increment, refactor labels, atualizar dashboards que dependiam de `identity`.

### 🎯 P4.A.3 — Multi-cluster constant labels

Per `multicluster-label-strategy` steering: `cluster` (k8s name) **e** `eks_cluster` (env) precisam estar em todas as séries antes de qualquer multi-cluster deploy.

Hoje:
- `cluster` em `build_info` apenas, e em alertas Alertmanager
- `eks_cluster` em lugar nenhum

**Solução**: constant labels no `prometheus.NewRegistry()`:

```go
constLabels := prometheus.Labels{
    "cluster":     os.Getenv("CLUSTER_NAME"),
    "eks_cluster": os.Getenv("EKS_CLUSTER"),
}
prometheus.WrapRegistererWith(constLabels, registry)
```

`EKS_CLUSTER` env var precisa ser adicionada em `config.yaml` + `compose.yaml` + `.env.example`. Já tem `CLUSTER_NAME`.

**Effort**: ~30min.

### 🎯 P4.A.4 — Dashboard refresh pós P4.A.1+P4.A.2

Após bug fixes e cardinality cleanup estarem em prod ≥30min observando:

- 🟢 Active alerts: trocar query Loki `count_over_time` por Prom counter rate (`rate(staffops_ad_alert_alerts_fired_total[5m])`) — mais rápido e previsível
- ➕ Adicionar painel **Workers up**: `staffops_ad_controller_workers_available`
- ➕ Adicionar painel **Cardinality watch**: `count by (__name__) ({__name__=~"staffops_ad_.+"})` — alerta visual se algum metric explodir
- ✅ Manter Recent alerts (Loki json) — é o ground truth detalhado

**Effort**: ~30min.

### 📌 P4.A.5 — OTel SDK adoption (defer pra Phase 6)

Skill `grafana-cross-signal-correlation` recomenda Loki `derivedFields` com regex pra `traceID=(\w+)`. Hoje controller usa `slog` standalone — sem OTel SDK, sem traceID nos logs. Wiring de OTel SDK é escopo grande (~3h+) e fora do MVP.

**Defer pra Phase 6** (Self-monitoring) — não bloqueia deploy.

---

## Phase 5 — Cluster readiness (was Phase 4)

Pré-requisito: P4.A.1 a P4.A.4 (P4.A.5 não bloqueia).

### 🎯 P5.1 — K8s Lease leader election (was P4.1)

Multi-replica controller with K8s Lease-based leader election. Required for cluster HA.

**Status**: pending since v0.5.0. Code pre-wired (`metrics.IsLeader`, `LeaderTransitions`, config has `lease_name`/`lease_namespace`).

**Effort**: small (use `k8s.io/client-go/tools/leaderelection`). Candidato a spec próprio se for combinado com Phase 5 todo em `.kiro/specs/production-readiness/`.

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

### 🎯 P6.3 — OTel SDK adoption (was P4.A.5)

Wire OTel SDK into controller + workers + ML for traceID-correlated logs. Then re-add Loki `derivedFields` per `grafana-cross-signal-correlation` skill, enabling log → trace navigation in Grafana.

**Effort**: ~3h. Cross-signal correlation only matters when there's enough multi-component activity to correlate — defer until cluster deploy proves stable.

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
