# Roadmap

Planned improvements for `staffops-anomaly-detection`. Ordered by priority — items higher up are nearer-term.

---

## Phase 0 — Strategic gates (BLOCKS algorithm work)

Established 2026-06-14 after a four-round adversarial design review. Conclusion: the
per-series univariate z-score detector is **commodity** (see `docs/architecture/decisions.md`
Decision 8). Whether there is a defensible product — **causal incident origination for
.NET/Python/Go** — is a **gated hypothesis** (`docs/hypothesis-causal-origination.md`),
not yet decided.

**These gates block any detector-core change (Holt-Winters, multivariate, etc.).**
Swapping the engine without measurement is re-shipping a detector you can't evaluate.

### 🎯 P0.1 — Measurement gate (synthetic fault injection on replay)

Inject synthetic latency/error faults over known-clean replay windows → produces a
recall lower-bound and FP upper-bound **without** needing labeled historical incidents.
Infra already exists (replay mode). This is the cheapest, highest-value item and the
prerequisite for everything algorithmic. **Without numbers, every detector swap is faith.**

**Spec**: [`specs/synthetic-injection/`](specs/synthetic-injection/) — spec ready, not executed.

### 🎯 P0.2 — Competitive teardown experiment

Time-boxed (days, not a slide): try to reproduce the surviving value as (a)
`predict_linear` rules in the existing `vmrules.yaml` and (b) a Robusta playbook.
Ports cheaply → it was config, ship that and stop. Resists → the causal core is found
empirically. This decides whether there is a product to build at all.

**Spec**: [`specs/competitive-teardown/`](specs/competitive-teardown/) — spec ready, not executed.

### 🎯 P0.3 — Validate the degradation model

Confirm the causal chains in `docs/architecture/degradation-model.md` against real
incidents via replay (walk each chain backwards from symptom, check leading→lagging
ordering). Until done, the model is a written hypothesis, not ground truth.

### 🎯 P0.4 — FDR (Benjamini-Hochberg) over per-cycle series

Independent of which thesis wins: ~400 adaptive series at fixed z>3 ≈ ~1000+ FP/day
from multiple comparisons alone. Apply FDR control before dispatch. Cheap, attacks the
largest FP source. **Read as diagnostic**: that the best detector fix is a generic
statistical correction unrelated to .NET/k8s/trace confirms the value was never in the
detector.

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

**Por que escopo grande**: requer Tempo integration, possível storage de correlation matrices, modelos de causalidade. Fase 3+ provavelmente. Candidato a spec próprio em `specs/cross-workload-dependency-mapping/`.

### 🎯 P2.7 — Falco integration (runtime security signal)

Adicionar Falco como **nova fonte de sinal** ao lado de métricas (VM), logs (Loki) e eventos do K8s. Falco detecta comportamento suspeito em runtime via eBPF (syscalls): shell em container, escrita em path sensível, escalação de privilégio, conexão de rede inesperada, mudança de binário. Esses eventos são ortogonais às anomalias de recurso/latência atuais e enriquecem a correlação com contexto de **segurança**.

**Valor**: hoje o controller vê "pod X com CPU/erro anômalo". Com Falco, pode cruzar "pod X anômalo **E** Falco disparou `Terminal shell in container` no mesmo pod no mesmo período" → eleva severidade e muda a natureza do alerta (possível comprometimento, não só saturação).

**Caminho de ingestão** (a decidir no design — respeitar `observability-principles.md`, sem export direto pra backend):
- **Opção A (preferida)**: Falco → Falcosidekick → saída para sink que o controller consome. Duas sub-opções:
  - Falco events como **logs** via OTel Collector → Loki, e o controller consulta via LogQL (reusa o pipeline de log rate já existente). Menor acoplamento.
  - Falcosidekick → webhook/HTTP receiver no controller (`internal/falco/`), tratado como detector `detector="falco"`.
- **Opção B**: Falco gRPC Outputs API consumida diretamente pelo controller (streaming). Mais acoplado, mais tempo-real.

**Componentes prováveis**:
- `internal/falco/` — ingestão + normalização de eventos (mapear `priority`/`rule`/`output_fields` para identidade pod/namespace/workload, reusando `correlation.ExtractWorkload`)
- Correlator passa a aceitar sinal `security` e a cruzar com janela temporal das anomalias existentes
- Enriquecimento: anotações `falco_rule`, `falco_priority`, link pra investigação
- Cardinalidade: **nunca** usar `rule`/`output` como label de métrica (alta cardinalidade — ver steering); só severidade/namespace/workload bounded; detalhe vai pra annotations/logs

**Decisões em aberto** (resolver na spec antes de implementar):
- Falco já está deployado nos clusters alvo? (se não, é pré-req de infra — delegar a `gitops`/`security`)
- Ingestão via Loki (pull) vs webhook/gRPC (push)?
- Falco apenas enriquece anomalia existente, ou pode disparar alerta sozinho? (escopo: começar só enriquecendo, evitar duplicar o alerting nativo do Falco)

**Pré-req**: Falco + Falcosidekick deployados no cluster (validar com `gitops`/`security`). Decisão de arquitetura de ingestão antes de codar.

**Effort**: large. Candidato a spec próprio em `specs/falco-integration/`.

**Spec**: [`specs/falco-integration/`](specs/falco-integration/) — requirements + design (4ª fonte de ingestão `Signal="security"`, ingestão Loki-pull default / webhook opt-in, enrich-not-alert v1, janela de correlação assimétrica) + tasks (Phase 0 pré-reqs de infra → core `internal/falco/` → correlator → testes → review sre/security).

**Status**: spec criada (2026-06-14) — bloqueada em Phase 0 (confirmar Falco/Falcosidekick deployados + decisão de ingestão A vs B). SRE/Security review pendente antes de implementar.

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

### Baseline robustness (from threat-model review, 2026-06-14)

Three independent gaps surfaced by [`docs/threat-model-and-limitations.md`](docs/threat-model-and-limitations.md). They degrade the **reliability** product, not just any future security framing — so they belong here, not in a security-only phase. All three are code-grounded (verified against `internal/baseline/store.go`).

#### 🎯 P2.8 — Workload-identity baseline keying

`baselineKey()` hashes the raw sorted label set. If `pod` is in the labels, the baseline **dies on every pod restart** (pods are cattle, ephemeral UIDs) → cold-start blindness on every rollout. `correlation.ExtractWorkload()` already exists (Deployment/StatefulSet/DaemonSet from pod name) but is used in correlation, **not** in the baseline key.

**Fix**: derive a stable entity key (workload, not pod) for baselining. Normalize the label set through `ExtractWorkload` before hashing; drop `pod`/ephemeral labels from the key.

**Watch-out**: legitimately per-pod signals (e.g. a specific replica leaking) would be smoothed — confirm the workload-level baseline still catches single-replica outliers, or keep a two-tier baseline (workload + per-replica deviation from siblings).

**Effort**: medium. Touches baseline key + warm-up semantics + tests.

#### 🎯 P2.9 — Outlier rejection before baseline update (anti-poisoning)

`Store.Evaluate()` updates EWMA/mean/stddev **unconditionally** on every sample, including anomalous ones. Consequences:
- **Benign**: a slow organic ramp is absorbed and stops alerting.
- **Adversarial**: low-and-slow activity drags the baseline until malicious load reads as normal (baseline poisoning).

**Fix**: gate the baseline update — e.g. skip (or down-weight) the update when the sample is already flagged anomalous beyond a hard multiple of stddev; optionally a "frozen baseline" window after a confirmed anomaly.

**Watch-out**: don't freeze so aggressively that genuine regime changes (intentional capacity increase) never re-baseline — pair with a slow-path acceptance after N sustained samples.

**Effort**: small-medium. Localized to `Evaluate()` + tests for the poisoning scenario.

#### 🎯 P2.10 — Absence-of-signal ("dead man's switch") detection

`/readyz` checks the detector's **own** dependencies, not whether an *expected* signal went silent. "This workload always emits N logs/min and stopped" raises nothing today. Misses both adversarial pipeline-blinding and genuine reliability events (a crashed exporter looks like calm).

**Fix**: track expected emission rate per workload/series; alert when a previously-active series goes silent for > threshold. Reuses the baseline store (a series with history that drops to zero samples is the signal).

**Watch-out**: legitimate scale-to-zero (KEDA, cronjobs idle) must not page — needs the same suppression/seasonality awareness as the positive-direction detectors. Cardinality: bound by tracked-series count, same guard as P5.4.

**Effort**: medium. New detector mode + careful FP control.

---

## Phase 3 — Operational maturity

### ✅ ~~P3.1 — Replay mode~~ — **DONE**

Spec at [`specs/replay-mode/`](specs/replay-mode/). All 16 tasks complete.

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

**Effort**: large. Requires Slack interaction handler + feedback store. Candidato a spec próprio em `specs/feedback-loop/`.

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

## Phase 5 Pre-Reqs — Production Hardening (BLOCKS Phase 5 deploy)

Established 2026-06-16 after multi-specialist evaluation (`dev`, `security`, `gitops`,
`anomaly-detection` reviewed in parallel). Conclusion: the project is intellectually
mature (the threat-model and Decision 8 stand up to independent review) and the
algorithmic backlog is correctly framed by Phase 0 — but **production engineering is
behind** the steering gates. The items below are **template work, not architecture**;
all eleven block any cluster deploy and have nothing to do with the Phase 0 strategic
gates. They can be executed in parallel with Phase 0.

> Why a dedicated section: the original roadmap implied this was scoped inside P5.2
> ("Deploy to cluster"), but the evaluation surfaced eleven **distinct hard-fails**
> that would each be rejected by Kyverno admission. Bundling them inside one bullet
> hid both the blast radius and the effort.

Spec: [`specs/production-hardening/`](specs/production-hardening/) —
requirements + tasks. No `design.md` because there is no architectural decision; this
is enforcement of existing steering rules (`k8s-best-practices.md`, `cloud-security.md`,
`12-factor-app.md`, `dev-environment.md`).

### Hard-fails (Kyverno admission blockers)

| # | Item | Source review |
|---|------|---------------|
| PH.1 | 🟡 Images run nonroot (`USER 65534`); pod-level `securityContext` (runAsNonRoot, readOnlyRootFilesystem, drop:[ALL], allowPrivilegeEscalation:false) for controller/worker/redis/ML still pending in manifests/Helm (PH.15) | security, gitops |
| PH.2 | 🟡 CI builds SHA-tagged images to `ghcr.io`; `:latest`/`REPLACE_ME_REGISTRY` removal in manifests pending Helm (PH.15) | security, gitops |
| PH.3 | Migrate base images to BDC golden (apko-built, cosign-signed): `golang`, `alpine`, `redis`, `python` | security |
| PH.4 | Enable Redis AUTH; mount password as file-secret via External Secrets Operator (12-factor IV) | security |
| PH.5 | ✅ Done — multi-stage ML Dockerfile; runtime image drops `gcc`/`g++` and `grpcio-tools` | security |
| PH.6 | Add mandatory labels (`CostCenter`, `Environment`, `app.kubernetes.io/version`) to all pod templates | gitops, security |
| PH.7 | Add `preStop` hook (`sleep 5`) and `terminationGracePeriodSeconds: 30` to all deployments | gitops |
| PH.8 | Create K8s manifest for the ML service (today exists only in `docker-compose`) | gitops |

### Test & CI gates (steering `dev-environment.md` ≥90%)

| # | Item | Source review |
|---|------|---------------|
| PH.9 | 🟡 Go controller coverage **~89.4%** (`./internal/...`) → ≥90%: remaining laggards are `readiness` (~12%), `ml` (~29%), `leader` (~49%) | dev |
| PH.10 | Bring ML service coverage from **0% → ≥90%**: `ml/tests/` is currently empty (`__init__.py` 0 bytes). Need unit + gRPC integration tests for forecaster, multivariate, server | dev |
| PH.11 | ✅ Done — `replay/window_test.go` `TestParseWindow_MixedDurationAndTimestamp` fixed; full Go suite green | dev |
| PH.12 | 🟡 CI added as `.github/workflows/ci.yml` (GitHub Actions): `test (go+ml) → build/push to ghcr.io` with SHA tags + ssh-agent for the private module. The `≥90%` coverage gate is report-only until PH.9 lands. | dev |

### Org-neutrality completion (continuation of 2026-05-30 rename)

| # | Item | Source review |
|---|------|---------------|
| PH.13 | Move `github.com/karlipegomes/staffops-otel-libs/go` (personal repo, pseudo-version) to org repo with proper release tagging — `karli` username in import path is the same anti-pattern as the earlier `bigdatacorp` rename | security, dev |
| PH.14 | Move BDC-specific URLs out of `controller/deploy/redis.yaml` ConfigMap (`vm-cluster-vmselect.monitoring:8481`, `loki-gateway.monitoring:80`) into Helm values | gitops |

### Helm chart + GitOps (move from raw YAML to ApplicationSet)

Currently rated **GitOps maturity 1.5/5**. To unblock cluster deploy:

| # | Item | Source review |
|---|------|---------------|
| PH.15 | Create `helm-charts/anomaly-detection/` with `templates/` + `values.yaml` + per-env overrides; covers controller, worker, redis (with PVC), ML, RBAC, VMRule, VMServiceScrape, PDB | gitops |
| PH.16 | Create ArgoCD `ApplicationSet` (matrix: cluster × env) targeting the Helm chart | gitops |
| PH.17 | Add `argocd.argoproj.io/sync-wave` annotations so Redis comes up before controller/worker | gitops |
| PH.18 | Add `PodDisruptionBudget` (`minAvailable: 1` controller leader, `minAvailable: 2` workers) | gitops |
| PH.19 | Replace `prometheus.io/scrape` annotations with `VMServiceScrape` CRDs | gitops |
| PH.20 | Remove explicit CPU limits from controller/worker (ScaleOps manages; throttling risk on the 60s detection cycle is real) | gitops |

### Network & secrets

| # | Item | Source review |
|---|------|---------------|
| PH.21 | Add `NetworkPolicy`: redis ← only controller+worker; worker gRPC ← only controller; ML gRPC ← only controller | security |
| PH.22 | Pre-provision a zero-permission IRSA role for the controller ServiceAccount (`eks.amazonaws.com/role-arn` annotation). Scoped policies added later when ML S3 model storage lands | security, gitops |
| PH.23 | Worker RBAC: drop `events list/watch` (only the controller uses `EventWatcher`; this is a copy-paste from controller RBAC) | security |

### Dependency hygiene

| # | Item | Source review |
|---|------|---------------|
| PH.24 | ✅ Done — runtime `grpcio` bumped 1.62.1 → 1.65.4 (CVE-2024-7246 DoS); `grpcio-tools` stays 1.62.1 (build-time only) | security |
| PH.25 | Resolve duplicate dependency pinning in `ml/Dockerfile` (versions hardcoded in `RUN pip install` AND `pyproject.toml` — drift risk) | dev, security |

**Effort estimate**: 1-2 sprints focused work. None of these are research — they are
mechanical application of existing steering. They can be parallelized with Phase 0
(strategic gates) since the two paths don't conflict.

---

## Phase 5 — Cluster readiness (was Phase 4)

Pré-requisito: ~~P4.A.1 a P4.A.4~~ ✅ all done. P4.A.5 (OTel SDK) deferred to P6, não bloqueia.

**Additional pre-req added 2026-06-16**: ALL items in [Phase 5 Pre-Reqs](#phase-5-pre-reqs--production-hardening-blocks-phase-5-deploy)
above. P5.2 cannot proceed until those land — Kyverno admission alone rejects the
current manifests on at least 6 controls.

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

**Spec**: `specs/agent-api-integration/`

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
- **2026-05-30**: Adopted spec-driven workflow per `staffops_agent_definition/steering/spec-driven-workflow.md`. `specs/replay-mode/` is the pilot — design reviewed before implementation, 11 ambiguities decided up front.
- **2026-05-30**: Observability hardening promoted to dedicated phase (Phase 4). Three blockers identified (instrumentation bugs, `identity` label cardinality bomb, missing multi-cluster labels) that must be fixed before any cluster deploy. Renumbered: old Phase 4 → new Phase 5, old Phase 5 → new Phase 6.
- **2026-05-30**: Subagent tool (Kiro CLI parallel execution) verified non-functional in this environment (3 consecutive `No result` returns including minimal `summary`-only ping). Falling back to serial implementation by main agent. Will retry when environment changes.
- **2026-05-30**: `eks_cluster` BDC-specific label removed from app code (was added briefly during P4.A.3, then reverted). Per `observability-principles.md` steering: app emits only `service.name` (here, `cluster`); environment-specific labels (`eks_cluster`, `environment`, `team`, `region`) belong at the scrape layer. Implemented via `static_configs.labels` per scrape job in `scripts/observability/prometheus.yml` for local dev; production uses `vmagent externalLabels`. Documented in `controller/README.md` "Multi-cluster labels" section. App stays org-agnostic — same as the `bigdatacorp` rename earlier.
- **2026-05-30**: Created `code-review` subagent — rigorous quality-gate persona that reviews diffs against 7 gates (correctness, steering, idiomatic, readability, tests, performance, security). Does not implement; only reviews. Total subagent count now 10. Subagent tool spawning still broken in this env, but main agent can adopt the persona by reading the prompt directly.
- **2026-06-16**: Multi-specialist evaluation executed (`dev`, `security`, `gitops`,
  `anomaly-detection` in parallel via the now-functional subagent tool). Findings:
  GitOps maturity 1.5/5; Go controller coverage **35%** vs steering gate **≥90%**;
  ML service coverage **0%** (`ml/tests/` empty); 5 Kyverno hard-fails on the deploy
  manifests (no securityContext, `:latest` tag, redis no auth, ML compiler in prod
  image, non-golden bases); Threat-model scorecard corroborated 11/11 axes (7/22 total)
  by independent security review; `anomaly-detection` reviewer reaffirmed Decision 8
  (detector is commodity) and recommended P0.2 competitive teardown before any further
  algorithmic investment. Result: created **Phase 5 Pre-Reqs** section above (PH.1–PH.25)
  capturing the eleven hard-fails as explicit blockers, and `specs/production-hardening/`
  spec to track execution. The original P5.2 ("Deploy to cluster") was the bullet that
  hid all this — making it explicit prevents the same compression in the next pass.
