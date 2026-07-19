# Program: topological causal correlation — full phase map

> **What this document is**: the complete upfront mapping of every phase of the
> "cross-application correlation" program (dependency graph + metric ontology +
> inter-service correlation), requested on 2026-07-16. It maps code, config,
> data, metrics, cardinality, risks, and validation criteria for each phase
> **before** any implementation.
>
> **What this document is not**: it is not a new product spec. The decision from
> the 2026-07-04 review ("no new product spec until P0.1/P0.2 run") remains in
> force — phases F3+ of this map are *gated* and reference the existing specs
> (`specs/service-dependency-graph/`, `specs/falco-integration/`) instead of
> creating new ones. Phases F0–F2 are fixes/config on top of what is already
> shipped and can run in parallel with the gates.

---

## 1. Verified facts (2026-07-16, live Prometheus + code)

Everything below was checked against production Prometheus and the code on
`main` — none of it is assumption:

### 1.1 The service graph already exists as metrics, and is richer than the spec assumed

`traces_service_graph_request_total` (+ `_failed_total`, `_client_seconds_*`,
`_server_seconds_*`) is live with **~17 unique client→server pairs** and carries
labels the `service-dependency-graph` spec (written before this verification)
did not anticipate:

| Label | Real example | Implication |
|---|---|---|
| `client` / `server` | `DataPlatform.People` → `dcp-cnj-api-btc` | primary edge identity |
| `client_service_workload` / `server_service_workload` | `dpm-people-api-prd` | **the service_name → workload join ships ready-made on the edge** |
| `client_service_namespace` / `server_service_namespace` | `dpm`, `dcp-btc` | same for namespace |
| `client_k8s_deployment_name` / `server_k8s_deployment_name` | `dcp-cnj-api-btc` | present when the peer runs on K8s |
| `client_eks_cluster` / `server_eks_cluster` | `prd`, `ecs`, `core` | **edges cross clusters** (prd↔ecs confirmed) |
| `connection_type` | `""`, `virtual_node`, `database`, `messaging_system` | separates instrumented peer, external dependency, DB, queue |
| `virtual_node` | `client` / `server` | the uninstrumented side of the edge |
| `__metrics_gen_instance` | metrics-generator pod | **every query must aggregate with `sum by (...)`** or edges triple |

**Virtual nodes are a strategic bonus**: uninstrumented external dependencies
(`metadata-api.bdc.internal`, `plataforma.bdc.internal`, `redis`,
`people.kyc_v1`…) show up as nodes. If every client of a virtual node degrades
simultaneously, the root is the external dependency — a diagnosis no
per-service detector can produce.

### 1.2 Spanmetrics have NO workload label

`spanmetrics_apm_calls_total{service_workload!=""}` returns empty — spanmetrics
anomalies identify only by `service_name` (the note at `config.yaml:118` is
correct). The identity resolver (F2) is still needed; what changed is that
**the service graph labels are the join table** (service_name →
workload/namespace/cluster) — no extra source needs to be built.

### 1.3 The real FDR defect is a truncated family, not pipeline position

`cmd/controller/main.go:265-287`: FDR runs **before** correlation (the
known-bugs table in AGENTS.md says "post-correlation" — inaccurate). The reason
it rejects ~0 is statistical: Benjamini-Hochberg only receives the anomalies
the workers already fired (z>3), i.e. a **censored** family of m≈half a dozen
p-values all <0.0027. The BH procedure over a truncated family accepts nearly
everything by construction. The fix requires knowing **how many tests were
executed** (~400 adaptive series/cycle), not just how many fired.

### 1.4 Edge anomalies would correlate as "_unknown_" today

`correlation/workloadKey()` (correlator.go:378) identifies by
`pod` → `service_name`. An edge anomaly has `client`/`server` and neither of
those → **all edge anomalies would collapse into a single `_unknown_` group**.
Edge rules (F1) are not 100% config-only: they require a touch in the
correlator.

### 1.5 Falco is not deployed

Zero `falco*` metrics in Prometheus — the Phase 0 blocker in
`specs/falco-integration/` is confirmed. F7 depends on infrastructure that does
not exist yet.

---

## 2. Target pipeline

```
Workers ──anomalies──► FDR (full family)               [F0]
                          │
                          ▼
                    Correlator phase A (identity)       [today]
                     + causal ranking via ontology      [F4]
                          │
                          ▼
                    Correlator phase B (topology)       [F5]
                     graph: internal/graph              [F3]
                     identity: NodeIndex                [F2]
                     merge → incident, root, blast radius
                          │
                          ▼
                    ML graph-aware (graph features)     [F6]
                          │
                          ▼
                    Dispatch (1 incident, N annotated symptoms)

New sources: service graph edges [F1], change-aware events,
Falco, Istio/containers [F7]. Learning layer on top [F8].
```

---

## 3. Phases

Gate legend: 🟢 runnable now · 🟡 runnable now with a caveat · 🔒 gated
(P0.1/P0.2 + 2026-07-04 decision) · ⛔ blocked on external infrastructure.

---

### F0 — FDR with the full family 🟢 ✅ SHIPPED (2026-07-17)

**Status**: implemented and merged. Workers report `adaptive_series_tested` on
`JobResults`; `fdr.Apply(anomalies, totalTests)` corrects over the true family;
gauge `staffops_ad_detection_fdr_family_size` added; replay applies FDR per tick
and reports `fdr_rejected` (also fixed a replay cross-metric contamination bug).
Full Go suite green, coverage 90.9%. **Remaining**: run the replay before/after
comparison to produce the FP-reduction number (this is P0.4's T6-T7).

**Goal**: make BH actually work — before F0 it was decorative (rejected ~0).

**Why first**: every later phase adds *more* anomaly sources (edges, events,
security). Stacking volume on top of a broken FP control amplifies the
12.8k alerts/day problem.

| Aspect | Mapping |
|---|---|
| Diagnosis | BH receives a censored family (only z>3 from workers); real m ≈ number of series evaluated per cycle (~400) |
| Fix | workers report `tested_series` (count of adaptive evaluations in the job) in `JobResult`; controller passes external `m` to `fdr.Apply` |
| Touchpoints | `proto/worker.proto` (+field on `JobResult`, regen stubs), `cmd/worker/main.go` (count evaluations), `internal/detection/fdr.go` (`Apply(anomalies, totalTests int)`), `cmd/controller/main.go:282` (sum `tested_series` across results) |
| Config | none new — `controller.fdr_target` already exists |
| Metrics | existing (`fdr_accepted/rejected_total`); add `staffops_ad_detection_fdr_family_size` (gauge) to observe m |
| Validation | replay before/after (this is exactly the pending T6-T7 of P0.4); expect `rejected > 0` in cycles with many marginal anomalies (z between 3.0–3.5) |
| Risk | larger m ⇒ more conservative BH ⇒ may suppress marginal TPs. Mitigation: measure recall via the P0.1 harness (direct synergy) |
| Effort | S (1–2 days) — the proto change is the most annoying part |
| Docs | `detection/` (FDR), `reference/metrics.md`, CHANGELOG |

---

### F1 — Detection on graph edges (config + correlator touch) 🟡

**Goal**: detect "degrading dependency" directly, without an in-memory graph
yet — just adaptive rules over the edge metrics.

**New rules** (`detection.adaptive_metrics`):

```yaml
# failure rate per edge — the most direct propagation signal there is
- name: edge_failure_rate
  query: sum(rate(traces_service_graph_request_failed_total[1m])) by (client, server, server_eks_cluster, connection_type)
  group_by: [client, server, server_eks_cluster, connection_type]

# server-side p99 latency per edge — degradation as seen by the caller
- name: edge_latency_p99
  query: histogram_quantile(0.99, sum(rate(traces_service_graph_request_server_seconds_bucket[5m])) by (le, client, server, server_eks_cluster))
  group_by: [client, server, server_eks_cluster]
```

Plus a safety static rule:

```yaml
# edge with sustained ~100% failure (failure rate == total rate).
# MUST gate on a minimum request rate (`and ... > 0.1`): these metrics are
# generated POST-tail-sampling (errors kept at 100%, normal traffic sampled
# down), so on a low-traffic edge the sampled population can be error-only and
# the ratio reads ~1.0 with a tiny real error rate. See §7.1 sampling-bias table.
- name: edge_total_failure
  query: (sum(rate(traces_service_graph_request_failed_total[5m])) by (client, server) / sum(rate(traces_service_graph_request_total[5m])) by (client, server)) and (sum(rate(traces_service_graph_request_total[5m])) by (client, server) > 0.1)
  threshold: 0.9
  operator: ">"
  severity: critical
```

> The full researched rule catalog — including edge rules beyond these three,
> unbiased SLI rules, runtime saturation, containers, Istio, and pipeline
> self-health — lives in [§7](#7-candidate-rule-catalog-research-pass-2026-07-17).

| Aspect | Mapping |
|---|---|
| Query precondition | `sum by` WITHOUT `__metrics_gen_instance` (otherwise 3× edges); test `_failed_total` — it only exists for edges that have failed at least once (sparse counter) |
| Required code (the "🟡") | `correlation/workloadKey()`: fallback `pod → service_name → server` so edges don't collapse into `_unknown_`; `enrichment.IdentityFromLabels`: recognize edge identity; `main.go` AnomalyByWorkload: `workload = server` |
| Edge identity | group by the **server** (the degraded side); `client` becomes an annotation. N edges of the same server anomalous = N impacted clients = root signal (this pre-buys part of F5 in the current correlator for free: same server ⇒ same group) |
| Cardinality | ~17 edges today × 3 rules ≈ ~50 new baseline series — trivial (guard limit: 10k from P5.4) |
| Baseline | same current guarantees (warm-up 60, anti-poison, seasonal). Sparse edges (rate 0 almost always) drive stddev→0: verify the stddev-floor behavior with zero-dominated series **before** enabling `edge_failure_rate`; the alternative is starting with `edge_latency_p99` + static only |
| Validation | 24h replay with the new rules (`--replay --config=candidate.yaml`) before touching the cluster — zero side effects; measure added volume/day |
| Risk | FP storm on low-traffic edges (small integral rate ⇒ unstable z). Mitigation: minimum-rate filter in the query itself (`> 0.1` via `and` clause) or `EXCLUDE_ADAPTIVE_WORKLOADS_CSV` |
| Effort | S–M (config + ~30 Go lines + workloadKey tests) |
| Docs | `configuration/rules`, `detection/adaptive`, CHANGELOG |

---

### F2 — Identity resolver (NodeIndex) 🟡

**Goal**: unify the three identities that coexist today with no join:

1. **Infra**: `cluster/namespace/pod` (runtime adaptive, static, events, logs)
2. **Service**: `service_name` (spanmetrics — no namespace, no workload)
3. **Edge**: `client`/`server` (service graph — F1)

**Finding that changes the design**: the service graph itself carries the
mapping (`server` = service_name; `server_service_workload` = workload;
`server_service_namespace` = namespace; `server_eks_cluster` = cluster). The
NodeIndex is **derived from the graph**, not from an extra source.

| Aspect | Mapping |
|---|---|
| Structure | `internal/topology/identity.go` — `NodeIndex: map[serviceName]NodeIdentity{Workload, Namespace, Cluster, IsVirtual}` |
| Population | same periodic query as the Discoverer (F3); standalone in F2, one dedicated query 1×/5min: `group by (server, server_service_workload, server_service_namespace, server_eks_cluster) (traces_service_graph_request_total)` + the client-side equivalent |
| Fallback | service outside the graph (no recent traffic): try `ExtractWorkload(service_name)` (many service_names ALREADY are the workload name, e.g. `dcp-cnj-api-btc`); otherwise identity stays service-only as today |
| Consumers | `correlation.workloadKey()` (unify groups: spanmetrics anomaly for service X + pod anomaly for X's workload → SAME group — immediate correlation win with no graph at all), `enrichment.IdentityFromLabels`, `main.go` AnomalyByWorkload (kills the spanmetrics `namespace="unknown"`) |
| Cache | controller memory (leader-only), 5min refresh; Redis optional only if replay needs it |
| Cardinality | zero mandatory new metrics; optional `staffops_ad_topology_nodes_total` (gauge, no labels) |
| Risk | service_name collision across clusters (e.g. `DataPlatform.People` on prd AND btc with different workloads) — **confirmed in the data**: the index key must be `(cluster, service_name)`, or `(service_name, deployment_environment)` when the cluster coincides (the real btc/prd case) |
| Validation | unit test with fixtures from the real labels above; in homolog, measure the drop of the `namespace="unknown"` label in `anomalies_by_workload` |
| Effort | M (2–3 days with tests) |
| Docs | `architecture/components`, CHANGELOG |

---

### F3 — In-memory graph (`internal/graph/`) 🔒

**Spec already exists**: `specs/service-dependency-graph/` (T1–T20). This map
**amends** the spec with what the live verification changed — update
`design.md` before executing:

| Amendment | Reason (fact 1.1) |
|---|---|
| Edge stores `connection_type` + `virtual` flag | virtual nodes/DB/queue have different propagation semantics (a degraded DB affects all clients; a virtual node has nowhere to "propagate to") |
| Edge stores full identity for both sides | client/server_service_workload/namespace/eks_cluster — feeds the NodeIndex (F2) from the same poll |
| Every query uses `sum by` excluding `__metrics_gen_instance` | 3 metrics-generator replicas triple the series |
| Graph is multi-cluster by construction | prd↔ecs↔core edges exist; node key = `(cluster, service)` |
| Redis optional, not default | 17 edges fit in leader memory; Redis is only justified for replay and HA warm-standby — decide at execution time (the spec assumed a Redis hash as default) |
| `AdjacencyStore.GetNeighbors(node, direction)` also takes a `connection_type` filter | propagation through `database` ≠ through a synchronous call |

The rest of the spec stands: Discoverer (5min poll), stale-edge TTL (2×
refresh), `staffops_ad_graph_{nodes,edges}_total` +
`discovery_duration_seconds` metrics, node-state gauge for the Grafana Node
Graph (spec phases 3–4, T11–T16).

| Aspect | Mapping |
|---|---|
| Gate | 2026-07-04 decision: execution after P0.1/P0.2. The F1+F2 work produces the data that **informs** whether F3 is worth it (how many real multi-service incidents show up per week?) |
| Cardinality | `graph_edge_*` per (client, server): ~17 today, guard ceiling 2,500 (50 services²) — spec validation T13 |
| Effort | M–L (spec estimates phases 1+3; ~1 week with tests) |
| Docs | `architecture/`, `reference/metrics.md`, new dashboard, CHANGELOG |

---

### F4 — Metric ontology + intra-workload causal ranking 🔒

**Goal**: within a correlated group, point at the **probable cause** using
declared knowledge of per-runtime degradation chains — instead of listing N
anomalies with no hierarchy.

**No spec exists** — once the gates clear, this is a candidate for
`specs/metric-ontology/` (creating the spec is what the 2026-07-04 decision
freezes; the design below is the upfront map).

**Proposed format** (`controller/ontology.yaml`, hot-reload like config.yaml):

```yaml
# layer: causal order (lower = closer to root cause)
#   1=infra  2=runtime  3=mesh  4=app
# causes: names of RULES (static/adaptive/log) this rule can explain
ontology:
  memory_ratio:                {layer: 1, causes: [oom_kills, high_restart_rate]}
  dotnet_gc_pause_rate:        {layer: 2, causes: [latency_p99_by_service]}
  dotnet_heap_growth:          {layer: 2, causes: [dotnet_gc_pause_rate, memory_ratio]}
  dotnet_threadpool_saturated: {layer: 2, causes: [kestrel_queued_requests_high, latency_p99_by_service]}
  hikari_pool_pending:         {layer: 2, causes: [latency_p99_by_service, error_rate_by_service]}
  go_sql_waiting:              {layer: 2, causes: [latency_p99_by_service]}
  queue_depth:                 {layer: 2, causes: [latency_p99_by_service]}
  istio_error_rate_by_workload:{layer: 3, causes: [error_rate_by_service]}
  edge_failure_rate:           {layer: 3, causes: [error_rate_by_service, istio_5xx_rate_high]}
  latency_p99_by_service:      {layer: 4, causes: [istio_5xx_rate_high]}
  error_rate_by_service:       {layer: 4, causes: []}
  error_rate_by_namespace:     {layer: 4, causes: []}
```

**Ranking algorithm** (deterministic, explainable — no ML):

1. In the correlated group, build the sub-DAG of the rules present via `causes`.
2. Probable cause = node(s) with no anomalous predecessor in the sub-DAG; tie →
   lower `layer`; tie → higher |z|.
3. Output: annotations `probable_cause=<rule>`, `causal_chain=<a→b→c>`; the
   remaining anomalies become `symptom_of=<cause>`.
4. Anomalies with no relation in the DAG: current behavior (no downgrade — an
   incomplete ontology must never suppress signal).

| Aspect | Mapping |
|---|---|
| Touchpoints | `internal/correlation/ontology.go` (loader + acyclic-DAG validation at boot), `correlator.buildPodAlert`/`detectWorkloadPatterns` (ranking after grouping), `alert/` (new annotations) |
| Config | `ontology.enabled` (default false during rollout), file path |
| Metrics | `staffops_ad_correlation_causal_ranked_total{outcome=ranked|no_relation}` |
| Severity interaction | **v1 does not touch severity** (annotations only) — touching escalation is detector-adjacent and collides with the ADR-0010 freeze/the spirit of P0; v2 decides with P0.1 numbers |
| Validation | replay over known incidents: does the pointed cause match the post-mortem? Requires P0.3 (degradation-model validation) — the chains in `degradation-model.md` are exactly the ontology's initial content |
| Risk | a wrong ontology produces wrong confidence (worse than nothing). Mitigation: v1 is annotation-only, never suppression; initial content = chains already written in `docs/architecture/degradation-model.md` (do not invent from scratch) |
| Effort | M (3–5 days) |
| Docs | new page `detection/causal-ranking.md`, `configuration/`, CHANGELOG |

---

### F5 — Inter-service correlation (incident merge) 🔒

**Goal**: the program's end target — N alerts from neighboring services in the
graph → 1 incident with root + blast radius. Maps to Phase 2 of the
`service-dependency-graph` spec (T6–T10), with amendments:

**Root rule (decision order, executed in Flush after phase A):**

1. **Dominant server-side**: `server=X` with an anomalous edge (F1) + local
   anomaly on X (any detector) + ≥1 client of X anomalous ⇒ root = X.
2. **Virtual node**: all anomalous edges point at virtual server V
   (uninstrumented) ⇒ root = "external dependency V" — dedicated alert
   (`kind: external-dependency`), no target workload.
3. **Temporal (spec T7 fallback)**: no anomalous edge, but graph neighbors
   anomalous within the same window ⇒ root = earliest timestamp, with
   `propagation_confidence` proportional to (time gap, hop count).
4. **No determinable root**: keep alerts separate + cross
   `related_incidents` annotation (never suppress without confidence).

| Aspect | Mapping |
|---|---|
| Hard prerequisites | F1 (edges as signal), F2 (identity join), F3 (neighborhood), F0 (volume under control) |
| Touchpoints | `internal/correlation/` (phase B in Flush), `CorrelatedAlert` gains `Kind: "incident"`, `RootWorkload`, `ImpactedServices []string`, `PropagationChain`; `alert/dispatcher` (incident payload); dedup fingerprint keyed by root (not by symptom — otherwise the symptom's 5min cooldown masks incident evolution) |
| Window | the 2min `correlation_window` may be short for multi-hop propagation (timeout → retry → failure takes minutes); map a dedicated `incident_window` (default 5min) — decide in the spec update |
| Metrics | `staffops_ad_correlation_incidents_total{root_type=service|external|temporal}`, `staffops_ad_correlation_symptoms_suppressed_total` |
| Cardinality | bounded — labels only severity/root_type; services go in annotations |
| Validation | **the whole program's criterion**: on replays of real incidents, % of multi-service incidents where the pointed root = post-mortem root; and alert reduction per incident (N→1). Without these two numbers F5 does not leave homolog |
| Risk | wrong suppression hides one real incident inside another. Mitigation: symptoms never disappear — they become incident annotations + structured log; rule 4 is the conservative default |
| Effort | L (1–2 weeks with tests) |
| Docs | `detection/correlation.md` (rewrite), `architecture/data-flow`, CHANGELOG |

---

### F6 — Graph-aware ML 🔒 (+ ADR-0010 freeze)

**Goal**: enrich the canonical vector with topological features so the
Isolation Forest can weigh propagation context.

| Aspect | Mapping |
|---|---|
| New features (candidates) | `fan_in` (number of clients of the service), `fan_out`, `anomalous_neighbors_fraction` (anomalous neighbors/total), `edge_failure_rate_max`, `replica_anomaly_fraction` (P2.5, already mapped in the ROADMAP), `is_hub` (fan_in > p90) |
| Touchpoints | `internal/ml/features.go` (BuildFeatureVector also receives `topology.NodeView`), `ml/server/multivariate.py` (CANONICAL_FEATURES 10 → 16: **contract break** — needs proto versioning or coordinated padding on both sides + paired redeploy) |
| Double gate | P0.1 (numbers) **and** ADR-0010 (ML escalation freeze until then). Also depends on F3/F5 in production generating the features |
| Risk | the structural defect flagged in the 2026-07-04 review remains (IF trains only on already-anomalous samples, no persistence) — adding features does not fix it; F6 only makes sense after P0.1 says whether the current ML adds anything |
| Validation | score distribution before/after; ablation (with/without graph features) on the P0.1 harness |
| Effort | M |

---

### F7 — New signal sources

**F7a — Change-aware suppression (K8s events)** 🟢 — already mapped as **P1.5**
in the ROADMAP ("the biggest k8s FP killer"). It belongs in this program
because a rollout is the *most common root cause of all*: anomaly + recent
rollout on the same workload (or on a **graph neighbor**, once F3 exists) ⇒
downgrade + `during_rollout=true` annotation. Runnable now in its P1.5 form
(no graph); the topological extension ("upstream neighbor's deploy") is
F5-dependent.

**F7b — Falco** ⛔ — complete spec in `specs/falco-integration/`, blocked on
infrastructure (verified: zero falco metrics in Prometheus). Prereq: deploy
Falco+Falcosidekick (delegate to `gitops`/`security`). Once unblocked: the
`security` signal enters the correlator as a 4th source (enrich-not-alert v1,
per the spec) and, with F4, gains a position in the ontology (layer 0 — a
compromise explains any symptom above it).

**F7c — Better container/Istio metrics** 🟡 — concrete candidates, all
verifiable in Prometheus before adding (same method as F1):

| Metric | Candidate rule | Value |
|---|---|---|
| `container_cpu_cfs_throttled_periods_total / container_cpu_cfs_periods_total` | adaptive by (namespace, pod) | throttling is a leading indicator of latency that cpu_ratio misses |
| `kube_pod_status_ready{condition="false"}` sustained | static | readiness flapping |
| `istio_request_duration_milliseconds_bucket` p99 per destination edge | adaptive | mesh-side latency complements the service graph (L7 vs trace) |
| `istio_tcp_connections_opened_total` rate anomaly | adaptive | connection leak |
| `kube_horizontalpodautoscaler_status_current_replicas` == max | static | HPA at ceiling = masked saturation |

Every addition follows the P1.4 catalog process (class, direction, owner, FP
budget) — no filled catalog entry, no rule.

---

### F8 — Learning layer 🔒 (last, deliberately)

Only after F5 is in production + the feedback loop (P3.3) is collecting ground
truth:

| Item | What it is | Prereq |
|---|---|---|
| Historical cross-series correlation | Pearson/DTW over windows to suggest edges traces can't see (batch→DB, cron→spike) — **suggests** an edge for human review, never creates one on its own | F3 (graph to compare against), window storage |
| Prophet (P2.2) | per-series forecast with real seasonality | baseline store must export time series (today stats only) |
| Threshold auto-tuning | per-rule z-threshold from feedback TP/FP | P3.3 |
| Learned edge weight | propagation confidence per edge from confirmed incidents | F5 + P3.3 |

**Anti-goal reaffirmed**: nothing in this phase enters the critical alerting
path without a deterministic fallback (documented anti-pattern: "ML in
critical alerting path without fallback").

---

## 4. Dependency and gating matrix

```
F0 (FDR) ─────────────┐
F1 (edges) ──► F2 (identity) ──► F3 (graph) ──► F5 (incident) ──► F6 (ML)
                                     ▲                ▲
P0.1/P0.2 (gates) ───────────────────┴────────────────┘
F7a (events) ─ independent (P1.5)                 F8 ─ after F5 + P3.3
F7b (falco) ─ blocked on infra       F7c (metrics) ─ P1.4 catalog per item
F4 (ontology) ─ after P0.3 validates the degradation model; slots between F2 and F5
```

| Phase | Gate | Can start |
|---|---|---|
| F0 | none (fix of a shipped item, P0.4's T6-T7) | **now** |
| F1 | none (config + identity fix; dry-run absorbs it) | **now**, after F0 |
| F2 | none (internal refactor, improves current correlation) | **now**, parallel to F1 |
| F7a | none (ROADMAP P1.5) | **now**, parallel |
| F3 | P0.1/P0.2 executed + existing spec updated | after gates |
| F4 | P0.3 (degradation model validated) + new spec (frozen until P0.1/P0.2) | after gates |
| F5 | F1+F2+F3+F0 · P0.1 numbers as comparison baseline | after gates |
| F6 | F5 + ADR-0010 revoked with numbers | last block |
| F7b | Falco infra | external |
| F8 | F5 + P3.3 | last block |

**Honest reading of the sequence**: the program's critical path is NOT code —
it is **executing P0.1** (already in progress, harness built). F0/F1/F2/F7a add
up to ~2 weeks of ungated work that improves the current system and produces
the data (edge volume, multi-service incident rate) that will justify — or
bury — F3/F5 with numbers instead of faith. This is consistent with the
2026-07-04 review's thesis: risk #1 is planning ahead of measurement.

---

## 5. Cross-cutting risks

| Risk | Phase | Mitigation |
|---|---|---|
| Alert volume grows before control does (12.8k/day + edges + events) | F1+ | F0 first; everything in dry-run; replay before every new rule |
| Baseline cardinality (edges × rules) | F1, F7c | trivial today (~50 series); P5.4 guard (10k) covers growth |
| Ambiguous multi-cluster identity (same service_name, different clusters/envs) | F2–F5 | composite key (cluster, service_name); real btc/prd case documented in §1.2 |
| Wrong suppression from misattributed root | F5 | symptoms never disappear (annotations); conservative rule 4; post-mortem validation |
| Stale ontology becomes misinformation | F4 | v1 annotates only; content comes from the validated degradation model (P0.3); quarterly review |
| ML vector contract (10 fixed features) | F6 | version the proto; paired controller+ml redeploy |
| Tempo metrics-generator dependency (if it dies, the graph freezes) | F3+ | edge TTL + fallback: correlator degrades to current behavior (phase A) — degradation is already the system's model |

---

## 6. Program success criteria (measurable)

1. **F0**: `fdr_rejected_total > 0` in normal operation; FP↓ measured in replay
   without recall↓ (P0.1 harness).
2. **F1**: ≥1 real degraded-dependency incident detected by an edge rule before
   the equivalent per-service alert (lead time > 0).
3. **F2**: `namespace="unknown"` in `anomalies_by_workload` drops >80%; unified
   correlation groups (spanmetrics+infra of the same workload) observable in
   logs.
4. **F5**: on multi-service incidents in replay/production: alert reduction
   N→1 of ≥60% and correct root ≥70% vs post-mortem. Below that, F5 does not
   leave homolog.
5. **Program**: alerts/day in dry-run go ↓ (not ↑) despite more sources — the
   definitive test that correlation is compressing, not adding.

---

## 7. Candidate rule catalog (research pass, 2026-07-17)

Researched against: live Prometheus inventory (every metric below
**verified present** on 2026-07-17 unless flagged ❌), Tempo service-graph
documentation, OTel semantic conventions v1.43, Google SRE workbook alerting
patterns, and the org skills `trace-derived-metrics` /
`apm-metrics-cross-runtime`. All rules are **PLANNED** — none are deployed.
Every rule must pass the P1.4 signal-catalog process (class, direction, owner,
action, FP budget) before entering `config.yaml`, and each addition goes
through replay first.

### 7.1 Ground rules from the research (apply to every rule below)

| Principle | Consequence for rule design |
|---|---|
| **Service-graph metrics are post-tail-sampling** (errors kept ~100%, normal traffic downsampled) | Absolute error *ratios* on edges are inflated — invalid as SLI. Valid uses: topology, relative comparison, adaptive baselines (bias is stable), failure-rate *trends*. Any edge ratio rule needs a minimum-rate gate. |
| **SDK HTTP metrics see 100% of traffic** (`http_server_request_duration_seconds` — verified present) | This is the **unbiased** RED source. Prefer it over spanmetrics/edge metrics for anything ratio- or SLI-shaped. The generation point of `spanmetrics_apm_*` (pre- or post-sampling) must be verified before trusting its ratios. |
| Symptom first, cause second (alerting-strategy) | RED rules (latency/error at the service edge) page; USE rules (runtime saturation) are *leading indicators* that should mostly **annotate/correlate** (ontology F4), not page on their own. |
| `rate()` window ≥ 4× scrape interval | 30s scrape ⇒ minimum `[2m]`; the `[1m]` windows in today's config ride the edge — new rules use `[2m]`/`[5m]`. |
| Aggregate buckets before `histogram_quantile`, never quantile-of-quantile | All p99 queries below follow `histogram_quantile(0.99, sum by (le, ...) (rate(...)))`. |
| Direction-of-badness (P1.6) | Every adaptive rule below declares `up_bad` / `down_bad` / `both_bad`. Until P1.6 ships, note it as a comment — it becomes config once the knob exists. |
| Bounded cardinality | Every `by()` clause uses bounded labels only (service_name, namespace, edge pairs — ~17 today, O(n²) ceiling watched via `tempo_metrics_generator_registry_active_series`). |

### 7.2 Tier A — service graph / edge rules (F1 scope)

The three F1 rules above, plus:

```yaml
# A4 — mesh/network overhead per edge: client-perceived minus server-side p99.
# Sampling bias cancels in the delta (both sides sampled identically).
# A large positive delta = network / Istio ztunnel / connection-pool problem
# BETWEEN the services, invisible to either service's own metrics.
# direction: up_bad
- name: edge_mesh_overhead_p99
  query: >
    histogram_quantile(0.99, sum(rate(traces_service_graph_request_client_seconds_bucket[5m])) by (le, client, server))
    - histogram_quantile(0.99, sum(rate(traces_service_graph_request_server_seconds_bucket[5m])) by (le, client, server))
  group_by: [client, server]

# A5 — database edge latency: connection_type="database" isolates DB calls.
# Catches slow-query regressions per (service, db) pair without DB-side access.
# direction: up_bad
- name: edge_db_latency_p99
  query: histogram_quantile(0.99, sum(rate(traces_service_graph_request_server_seconds_bucket{connection_type="database"}[5m])) by (le, client, server))
  group_by: [client, server]
```

**Not** a detection rule but F3 input: a new edge appearing in
`traces_service_graph_request_total` (topology change) is graph-discovery
territory — surfacing "service X gained a dependency" belongs to the F3
Discoverer as an annotation, not to the anomaly pipeline.

### 7.3 Tier A — unbiased SLI rules (new finding: SDK HTTP metrics exist)

`http_server_request_duration_seconds_*` is emitted by the OTel SDKs (all
runtimes, 100% of traffic). These should become the **primary** RED adaptive
rules, with the existing spanmetrics rules kept until bias of
`spanmetrics_apm_*` is verified, then reconciled (running both = double
detection on the same symptom → double FDR load and correlated FP pairs):

```yaml
# A6 — unbiased error ratio per service. direction: up_bad
- name: http_error_ratio_by_service
  query: >
    sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[2m])) by (cluster, service_name)
    / sum(rate(http_server_request_duration_seconds_count[2m])) by (cluster, service_name)
  group_by: [cluster, service_name]

# A7 — unbiased p99 latency per service. direction: up_bad
- name: http_latency_p99_by_service
  query: histogram_quantile(0.99, sum(rate(http_server_request_duration_seconds_bucket[5m])) by (le, cluster, service_name))
  group_by: [cluster, service_name]

# A8 — client-side dependency latency (outbound calls, per service).
# Complements edge rules: sees 100% of traffic, but aggregated (no per-target
# split unless server_address label is bounded — verify before adding it).
# direction: up_bad
- name: http_client_latency_p99_by_service
  query: histogram_quantile(0.99, sum(rate(http_client_request_duration_seconds_bucket[5m])) by (le, cluster, service_name))
  group_by: [cluster, service_name]

# A9 — DB operation latency per service (OTel stable semconv, unbiased).
# The direct "is it the database?" signal for the F4 ontology (layer 2).
# direction: up_bad
- name: db_operation_latency_p99_by_service
  query: histogram_quantile(0.99, sum(rate(db_client_operation_duration_seconds_bucket[5m])) by (le, cluster, service_name))
  group_by: [cluster, service_name]
```

❌ Not available (verified absent): `rpc_server_call_duration_seconds` /
`rpc_client_call_duration_seconds` — no OTel RPC metrics in this environment;
gRPC visibility stays trace-only until instrumentations are bumped.

### 7.4 Tier B — runtime saturation (USE / leading indicators)

Ontology layer 2 (F4): these mostly *explain* RED symptoms. Recommended as
adaptive + `severity: info`-shaped annotations, not pagers.

```yaml
# B1 — Go scheduler saturation: goroutines waiting for CPU.
# p99 > ~10ms sustained = too many goroutines for GOMAXPROCS. direction: up_bad
- name: go_sched_latency_p99
  query: histogram_quantile(0.99, sum(rate(go_sched_latencies_seconds_bucket[5m])) by (le, cluster, namespace, pod))
  group_by: [cluster, namespace, pod]

# B2 — Go goroutine leak: monotonic growth is the signature (CUSUM/P1.9 is the
# right detector; z-score catches only step changes). direction: up_bad
- name: go_goroutines
  query: max(go_goroutines) by (cluster, namespace, pod)
  group_by: [cluster, namespace, pod]

# B3 — Node.js event loop lag p99 (THE Node saturation signal).
# Static complement: > 0.5s sustained = loop blocked. direction: up_bad
- name: nodejs_eventloop_lag_p99
  query: max(nodejs_eventloop_lag_p99_seconds) by (cluster, namespace, pod)
  group_by: [cluster, namespace, pod]

# B4 — Kestrel connection queue (pairs with existing kestrel_queued_requests
# static rule; connections queue BEFORE requests do). direction: up_bad
- name: kestrel_queued_connections
  query: max(kestrel_queued_connections) by (cluster, namespace, pod)
  group_by: [cluster, namespace, pod]

# B5 — DNS lookup p99: a classic hidden dependency; slow DNS shows up as
# client latency with healthy servers. direction: up_bad
- name: dns_lookup_p99_by_service
  query: histogram_quantile(0.99, sum(rate(dns_lookup_duration_seconds_bucket[5m])) by (le, cluster, service_name))
  group_by: [cluster, service_name]

# B6 — messaging consumer processing time (OTel semconv, Development
# stability — name may move; pin and revisit). direction: up_bad
- name: messaging_process_p99_by_service
  query: histogram_quantile(0.99, sum(rate(messaging_process_duration_seconds_bucket[5m])) by (le, cluster, service_name))
  group_by: [cluster, service_name]
```

### 7.5 Tier B — container / K8s rules (F7c scope)

```yaml
# B7 — CPU throttling ratio: the leading indicator cpu_ratio misses (a pod can
# throttle hard at 60% "usage"). Gate on sustained ratio. direction: up_bad
- name: cpu_throttling_ratio
  query: >
    sum(rate(container_cpu_cfs_throttled_periods_total{container!=""}[5m])) by (cluster, namespace, pod)
    / sum(rate(container_cpu_cfs_periods_total{container!=""}[5m])) by (cluster, namespace, pod)
  group_by: [cluster, namespace, pod]
  # static complement: > 0.25 sustained = requests/limits mis-sized (warning)

# B8 — HPA pinned at max: saturation masked by autoscaling "working".
# static, threshold 0 on the gap. severity: warning
- name: hpa_at_ceiling
  query: max(kube_horizontalpodautoscaler_spec_max_replicas - kube_horizontalpodautoscaler_status_current_replicas) by (cluster, namespace, horizontalpodautoscaler) == 0
  # fires when current == max (gap == 0), i.e. no headroom left

# B9 — deployment availability gap (generalizes the existing rollout_stuck,
# which only covers Argo Rollouts). static, threshold 0. severity: warning
- name: deployment_replicas_unavailable
  query: max(kube_deployment_status_replicas_unavailable{namespace!~"${EXCLUDE_NAMESPACES_REGEX:kube-system}"}) by (cluster, namespace, deployment)

# B10 — readiness flapping: pod oscillating Ready/NotReady without restarting
# (restarts rule misses it). adaptive on the transition rate. direction: up_bad
- name: pod_ready_flapping
  query: sum(changes(kube_pod_status_ready{condition="true"}[15m])) by (cluster, namespace, pod)
  group_by: [cluster, namespace, pod]
```

### 7.6 Tier B — Istio mesh rules

```yaml
# B11 — mesh-side p99 per destination workload: L7 view that pairs with the
# trace-derived edge view (A2); divergence between them is itself a signal
# (mesh sees what traces miss when sampling drops spans). direction: up_bad
- name: istio_latency_p99_by_workload
  query: histogram_quantile(0.99, sum(rate(istio_request_duration_milliseconds_bucket{destination_workload!=""}[5m])) by (le, cluster, destination_workload, destination_workload_namespace))
  group_by: [cluster, destination_workload, destination_workload_namespace]

# B12 — TCP connection churn: leak or retry storm signature. direction: up_bad
- name: istio_tcp_connection_rate
  query: sum(rate(istio_tcp_connections_opened_total[5m])) by (cluster, destination_workload, destination_workload_namespace)
  group_by: [cluster, destination_workload, destination_workload_namespace]
```

### 7.7 Tier A — signal-pipeline self-health (guards every edge rule)

If the Tempo metrics-generator degrades, every F1/A-tier edge rule silently
loses its data source. These two statics are the dead-man's-switch for the
edge signal itself (both metrics verified present):

```yaml
# A10 — generator dropping series = edges silently missing from the graph.
- name: servicegraph_series_limited
  query: sum(rate(tempo_metrics_generator_registry_series_limited_total[5m]))
  threshold: 0
  operator: ">"
  severity: critical

# A11 — edges expiring unpaired = broken context propagation somewhere
# (missing traceparent), or spans arriving too late. Baseline first — a small
# steady rate is normal; alert on the adaptive deviation, not absolute.
- name: servicegraph_expired_edges
  query: sum(rate(tempo_metrics_generator_processor_service_graphs_expired_edges[5m]))
  group_by: []
  # adaptive, direction: up_bad
```

### 7.8 Hygiene findings from the verification (act before adding anything)

| Finding | Evidence | Action |
|---|---|---|
| **Dead rules (5)** ✅ removed 2026-07-17 | Full audit of `config.yaml` metrics vs live inventory: `dotnet_gc_pause_rate`, `queue_depth`, `queue_failed_rate`, `go_sql_waiting`, `karpenter_scheduling_duration` reference absent metrics; `dotnet_threadpool_saturated` had a dead `or` half | Removed from repo `config.yaml` (with re-add notes); threadpool static simplified to the live metric |
| **Window too tight** ✅ fixed 2026-07-17 | `rate[1m]` < 4× a 30s scrape | `[1m]` → `[2m]` in repo config **and** chart default rules. Enrichment untouched (`*_1m` are ML canonical feature names) |
| **Rule-set drift repo ↔ chart** ✅ resolved for deploy 2026-07-18 | Deployed rules came from the chart's small default `detection:` (incl. `high_cpu_ratio`/`high_memory_ratio`, retired in the repo for duplicating VMAlert) | Ported the repo's tuned 18-rule set into the gotmpl `detection:` override on devops-core (6→18 rules, dropped the noisy cpu/mem statics). Homologated: 0 cycle errors, FDR family 277 (service-level, down from ~2500 pod-level), rejection ~18%. **SSOT decided 2026-07-19 (Decision 10)**: the gotmpl override is the deploy source of truth; `config.yaml` is the local/replay reference — they diverge by design, not drift. The chart *default* stays a minimal placeholder |
| **Cycle duration vs interval** ✅ found+fixed 2026-07-18 | The tuned set's spanmetrics/istio histogram queries pushed cycle p99 to ~58s against the chart's 30s `jobInterval` — cycles backed up, the self-monitoring "p99 > 30s" rule would fire | Set `controller.jobInterval: 60s` in the gotmpl (matches the repo's config.yaml intent). Steady-state cycle p99 dropped to ~20s with 40s headroom, cadence clean. Follow-up: the chart's 30s default is too tight for a rich rule set — consider bumping it or parallelizing worker query execution |
| **Unverified bias**: `spanmetrics_apm_*` | Generation point (pre- vs post-tail-sampling) not confirmed | Verify against collector config before trusting its error *ratios*; if post-sampling, migrate RED rules to `http_server_*` (§7.3) and demote spanmetrics to trend-only |

### 7.9 Rollout order for the catalog

1. **Hygiene first** (§7.8): dead rules out, windows fixed — zero new signal, less noise.
2. **A10/A11** (pipeline self-health): cheap statics that protect everything after.
3. **A1–A5** (edge rules): F1 proper — needs the correlator identity touch.
4. **A6–A9** (unbiased SLI): after verifying spanmetrics bias, reconcile or replace.
5. **B-tier**: one at a time, each through replay + P1.4 catalog entry, watching
   `staffops_ad_detection_fdr_family_size` growth (every new adaptive series
   enlarges the BH family — more rules make the FDR *stricter* for everyone,
   which is correct but must be understood when tuning `fdr_target`).
