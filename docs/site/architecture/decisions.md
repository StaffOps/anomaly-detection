# Design Decisions

Key architectural decisions and their rationale.

---

## Decision 1: Monorepo (controller + ml)

**Choice**: Keep Go controller and Python ML service in a single repository.

**Justification:**

1. Single docker-compose for the full stack — faster dev loop
2. Shared protobuf definitions without cross-repo sync
3. Versioning is independent per component anyway (controller 0.7.0, ml 0.2.0)

**Trade-offs:**

| Cost | Reality |
|------|---------|
| Larger repo | Acceptable — both components are small |
| Mixed language tooling | Docker handles both; no local SDKs needed |

---

## Decision 2: Controller-Worker split (not monolith)

**Choice**: Separate controller (orchestration) from workers (query execution).

**Justification:**

1. Workers are stateless and scale horizontally — more workers = more query throughput
2. Controller handles correlation/enrichment which needs global view of all anomalies
3. gRPC streaming allows efficient job dispatch without polling

**When this would be wrong:**

- If query volume is always low (< 50 queries/cycle) — overhead of gRPC not justified
- Currently at ~30 queries/cycle, so borderline. Justified by future growth.

---

## Decision 3: Redis for baselines (not in-process)

**Choice**: Store EWMA baselines and dedup state in Redis, not in worker memory.

**Justification:**

1. Workers are stateless — can restart/scale without losing baseline history
2. Dedup works across controller failovers (future HA with leader election)
3. Seasonal profiles accumulate over days — survives any single process restart

**Trade-offs:**

| Cost | Reality |
|------|---------|
| Network latency per baseline read | ~1ms per Redis call; acceptable at 30s cycle |
| Redis as SPOF | Acceptable for MVP; Redis Sentinel planned for prod |

---

## Decision 4: Dry-run as default mode

**Choice**: System starts in dry-run mode. Real alert dispatch requires explicit opt-in.

**Justification:**

1. Safe rollout — observe detection quality before alerting humans
2. Prevents alert flood during tuning phase
3. Allows running in parallel with existing alerting without noise

**When to disable dry-run:**

- Phase 4 observability hardening complete
- Top noisy workloads dashboard available (P3.2)
- Feedback loop active (P3.3)
- Operator has observed ≥1 week of dry-run output

---

## Decision 5: Env vars for all endpoints (12-Factor)

**Choice**: All endpoint URLs, cluster names, and namespace lists come from environment variables with `${VAR}` / `${VAR:default}` substitution in `config.yaml`.

**Justification:**

1. Same binary/config across environments (dev, staging, prod)
2. No org-specific values hardcoded in repo
3. docker-compose fails fast if required vars missing
4. Other organizations can adopt without forking

---

## Decision 6: `staffops_ad_*` metric prefix with sub-namespaces

**Choice**: 5 sub-namespaces for Prometheus metrics: `controller`, `worker`, `detection`, `alert`, `ml`.

**Justification:**

1. Clear ownership — which component emits which metric
2. Easy to build recording rules per concern
3. Avoids flat namespace collision as system grows

**Sub-namespaces:**

| Prefix | Owner | Examples |
|--------|-------|---------|
| `staffops_ad_controller_*` | Controller | `cycle_duration_seconds`, `workers_available` |
| `staffops_ad_worker_*` | Workers | `queries_total`, `baseline_series_tracked` |
| `staffops_ad_detection_*` | Detection | `anomalies_total`, `suppressed_total` |
| `staffops_ad_alert_*` | Dispatcher | `alerts_fired_total`, `dedup_hits_total` |
| `staffops_ad_ml_*` | ML client | `calls_total`, `multivariate_anomalies_total` |

---

## Decision 7: App emits only `cluster` label; environment labels at scrape layer

**Choice**: The application emits a single constant label `cluster` (from `CLUSTER_NAME` env var). Organization-specific labels (`eks_cluster`, `environment`, `team`) are added at the scrape/vmagent layer.

**Justification:**

1. App stays org-agnostic — same binary works anywhere
2. Per `observability-principles` steering: SDK responsibility is service identity; Collector/scrape layer adds environment metadata
3. Avoids coupling app code to org-specific label taxonomy

**Implementation:**

- Local dev: `static_configs.labels` per scrape job in `prometheus.yml`
- Production: `vmagent externalLabels` on remote_write

---

## Decision 8: The detector is commodity; the product is causal incident origination

**Status**: the *first* half below is a **decision** (proven, code-grounded). The
*second* half is a **gated hypothesis**, not a decision — it does not become an ADR
until its kill criteria pass. See `docs/hypothesis-causal-origination.md`.

**Decided (proven)**: per-series univariate z-score over EWMA is **commodity** and
cannot be the differentiator. Established across four review rounds at code level:

1. The market does point-wise anomaly detection better (burn-rate for anything with an
   SLO; mature multivariate elsewhere).
2. The dispatch path is literally Alertmanager (`dispatcher.go` POSTs to
   `/api/v2/alerts`); the horizontal workload-collapse is replicable with Alertmanager
   `group_by: [workload]`; enrichment+dedup is what Robusta/Keep already do.
3. The "exclusive niche" of leading saturation signals (`queue_depth`,
   `hikaricp_pending`, `heap_growth`) mostly dissolves into one-line `predict_linear`
   recording rules — saturation toward a known ceiling is *predictable*, not anomalous.
4. The univariate Z-score also has a severe own-goal: with `ewma_alpha=0.3` the EWMA
   center *chases the ramp*, so the detector goes blind during the rising edge of an
   incident — exactly when it matters.

**Consequence**: do not invest in improving the univariate detector as the product.
Treat it as one commodity input. Generic capabilities (detection, grouping, dispatch,
enrichment) should be delegated to / consumed from existing tools, not rebuilt.

**Hypothesis (NOT yet decided — gated)**: the defensible product is **causal incident
origination for the .NET + Python + Go stack** — explaining *what caused* an incident
and *what it will affect next* (the causal chain), grounded in a per-language
**degradation model** (`docs/architecture/degradation-model.md`). The candidate
irreducible moat is the **intra-runtime** causality (e.g. .NET threadpool starvation →
queue → latency → errors) that edge-level RCA tools (Causely, APM-native RCA) do not
see.

**Kill criteria for the hypothesis** (must pass *before* it becomes an ADR or before
building on it):

1. **Measurement gate** — synthetic fault injection over replay produces a real
   recall lower-bound and FP upper-bound. Without numbers, any detector swap is faith.
2. **Competitive teardown as experiment** (not a slide) — time-boxed attempt to
   reproduce the surviving value as (a) `predict_linear` rules in the existing
   `vmrules.yaml` and (b) a Robusta playbook. If it ports cheaply → it was config, and
   there is no product. If the *causal model* resists fitting into a playbook → the
   core is found empirically.
3. **Degradation model validated** — chains in `degradation-model.md` confirmed
   against real incidents via replay, not just asserted by mechanism.

**Open sub-decisions deferred** (not decided here):

- **Seam**: whether to re-cut the `baseline.Evaluator` interface (currently
  `Evaluate(series, scalar)` — univariate by construction) to accept a vector for
  low-dimensional, topology-aware multivariate detection — or freeze the detector as
  terminal commodity and build the causal layer above it.
- **Topology ingestion**: causal/vertical correlation needs service-graph (edge-level)
  data. **Correction to an earlier assumption**: this is *not* currently ingested —
  grep confirms zero `service_graph` references in the repo. The system ingests
  node-level RED (`spanmetrics ... by (service_name)`), not edges. So topology is a
  *new ingestion path*, not a fized wire.
- **FDR (Benjamini-Hochberg)**: worth doing early regardless of which thesis wins
  (cheap, attacks the largest FP source — ~400 series at fixed z>3). Read as a
  *diagnostic*: that the best concrete detector improvement is a generic statistical
  correction unrelated to .NET/k8s/trace is itself evidence the value was never in the
  detector.

**When the decided half would be wrong**: if a measurement (gate 1) showed the
univariate detector achieving competitive precision/recall on the curated golden
signals — then "commodity" would be too harsh. Considered unlikely given the
multiple-comparisons math, but it is the falsifier.

---

## Decision 9: Local homolog build deployed; CI publishes the versioned image as the record

**Choice**: The deployed image is the **locally-built** multi-arch image in Harbor
(`labs/staffops-anomaly-detection-*:<version>`). The CI `release.yml` (on a `v*` git
tag) builds and publishes the **same source** to Docker Hub as the immutable versioned
image + GitHub Release. The cluster keeps running the Harbor build; Docker Hub is the
provenance/record, not the deploy source.

**Justification:**

1. We homologate the local build *first*, then push the code that was validated. The
   pushed commit (and the `v*` tag CI builds from) is exactly the homologated source —
   the compiled binary is identical (post-homolog diffs are comments/tests only, which
   don't compile into the runtime image).
2. Repointing the deploy at Docker Hub adds a pull-path/registry-auth change with **no
   behavioural difference** — the running code is already the released code.
3. "Local is always ahead of what's pushed; we push *because* we homologated locally"
   (2026-07-19). The official path receives already-validated code.

**Trade-offs:**

| Cost | Reality |
|------|---------|
| Two registries (Harbor deploy, Docker Hub record) | Intentional. Harbor = what runs (homologated), Docker Hub = versioned provenance |
| Not GitOps-pure (deploy image ≠ CI artifact) | Acceptable for homolog; revisit when exiting dry-run / going to a fresh cluster that has no local Harbor build |

**Do not "fix" this by repointing the gotmpl at Docker Hub** unless the goal is
specifically registry consolidation — it changes provenance, not behaviour.

---

## Decision 10: `controller/config.yaml` (repo) and the gotmpl `detection:` (deploy) are allowed to diverge

**Choice**: The **deployed** detection rule set lives in the per-cluster Helm values
override (`k8s-setup/.../anomaly-detection/values.yaml.gotmpl`, `detection:`). The repo's
`controller/config.yaml` is the **local-dev / replay reference** set. They are NOT kept
in lockstep, and that is by design.

**Justification:**

1. The deployed set is tuned per cluster (18+ rules, Group A, per-workload suppression,
   direction-of-badness) and is GitOps-managed where the cluster config lives.
2. The repo `config.yaml` runs the local docker-compose stack and `--replay`; a smaller
   representative set is fine there.
3. Same philosophy as Decision 9: the deployed artifact is the source of truth for what
   runs; the repo file is a reference, not a mirror.

**Trade-offs:**

| Cost | Reality |
|------|---------|
| Rule tuning in `config.yaml` does NOT reach the cluster | Correct — cluster tuning goes in the gotmpl. Editing `config.yaml` only affects local/replay |
| Reader might expect them to match | Mitigated by this decision + the header note in both files |

**The gotmpl `detection:` block is the deploy source of truth.** Do not treat the
repo↔deploy difference as drift to reconcile.
