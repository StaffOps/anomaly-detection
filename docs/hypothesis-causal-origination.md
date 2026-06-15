# Hypothesis: Causal Incident Origination (gated)

> **This is a hypothesis, not a decision.** It is written down so it can be *killed or
> confirmed by evidence* — not so it can be believed. It becomes an ADR (in
> `decisions.md`) only after the kill criteria below pass. Until then, do not build
> production code on top of it beyond what the kill criteria require.

This document is the home for the "what is the product?" bet that emerged from four
rounds of adversarial review. The proven half (the univariate detector is commodity)
lives in `decisions.md` Decision 8. This is the unproven half.

---

## The bet, in one sentence

The defensible product is **causal incident origination for the .NET + Python + Go
stack**: when something breaks, explain *what caused it* and *what it will affect
next* — the causal chain — instead of emitting N independent "metric X is anomalous"
alerts.

## Why it might be real (the argument for)

- Generic RCA / AIOps tools (Causely, APM-native RCA) reason at the **edge between
  services** (A calls B). They do not encode **intra-runtime** causality — how a .NET
  threadpool, a Go GC, or a Python event loop degrades *inside one process*, in a
  specific order. That per-language, ordered knowledge is captured in
  `degradation-model.md` and is the candidate moat.
- The system already ingests the curated golden signals (RED via spanmetrics, USE-style
  saturation signals per language). The raw material for causal reasoning is partly
  there.

## Why it might be a trap (the argument against — kept honest)

- **Sophisticated naive attractor**: "multivariate to catch unknown-unknowns across
  all metrics" dies to the curse of dimensionality (Mahalanobis over ~400 series →
  distances concentrate → signal washes out). A decade of AIOps vendors drowned here.
  Multivariate is only tractable over a *small, curated, causally-coherent* vector (the
  RED of *one* service, 3-4 dims) — which itself requires knowing the topology.
- **Not greenfield**: causal/topological origination is a less-crowded category than
  "anomaly detection", but not empty (Causely, APM RCA). The teardown this idea owes is
  vs *those*, not vs Alertmanager.
- **The enrichment residue does not survive**: deep-links, metric lists, dashboards,
  Grafana UIDs are a Robusta playbook. Port them upstream; they are not product.
- **The only plausible survivor** is the intra-runtime degradation model — and only if
  encoded as *causal logic*, not as a list of metric names, and only at the
  intra-runtime level where edge-level playbooks don't reach.

## Kill criteria (must all pass before this becomes a decision)

| # | Gate | Pass condition | Status |
|---|------|----------------|--------|
| 1 | **Measurement** | Synthetic fault injection over replay yields a real recall lower-bound + FP upper-bound. (Infra already exists: replay mode.) | ⬜ not started |
| 2 | **Competitive teardown as experiment** | Time-boxed attempt to reproduce surviving value as (a) `predict_linear` rules in `vmrules.yaml` and (b) a Robusta playbook. Ports cheaply → it was config, no product. Resists → core found. | ⬜ not started |
| 3 | **Degradation model validated** | Chains in `degradation-model.md` confirmed against real incidents via replay, not asserted by mechanism. | ⬜ not started |

**If gate 2 shows the value ports cheaply into config**, the honest conclusion is:
there is no product to build — ship `predict_linear` rules + a Robusta playbook and
stop. That is a *successful* outcome of the experiment, not a failure.

## Deferred sub-decisions (depend on the gates)

- **Seam**: re-cut `baseline.Evaluator` to accept a vector (toward low-dimensional,
  topology-aware multivariate) vs freeze the detector as commodity and build the causal
  layer above it. Do not decide until gate 1 gives numbers.
- **Topology ingestion**: edge-level service-graph is **not currently ingested**
  (confirmed: zero `service_graph` refs in repo). This is a *new ingestion path* with
  real cost, not a loose wire to connect. Scope it only if gates justify the causal
  direction.
- **Observability gaps named by the model** (cheap, arguably worth doing regardless):
  Python event-loop lag, Go `go_goroutines`, Go GC CPU fraction — each is a
  root-cause leading signal currently invisible (see `degradation-model.md`).

## Suggested order of work (if pursuing)

```
0. Measurement gate (synthetic injection on replay)        ← do this FIRST
0. Competitive teardown experiment (predict_linear + Robusta playbook)
   └─ these two decide whether there is a product at all
─────────────── only if the gates justify it ───────────────
1. Validate degradation-model chains against real incidents
2. FDR (Benjamini-Hochberg) — cheap, worth it regardless; read as diagnostic
3. Decide the seam (vector Evaluator vs causal layer above)
4. Swap the detector core (Prophet already in repo / Holt-Winters) IF numbers justify
```

Note that swapping the detector core (Holt-Winters etc.) is **last**, behind the
measurement gate. Swapping the engine without the harness is re-shipping a detector you
can't evaluate — the original sin, with a fancier algorithm.

## Provenance

This hypothesis is the synthesis of an adversarial design review (four rounds,
2026-06-14). The reviewer's framing — *"what does your .NET/Python/Go stack know about
how it fails that no generic tool can know, and is it written down as a causal model or
only in someone's head?"* — is the question this whole bet answers. As of writing, the
answer was "only in someone's head"; `degradation-model.md` is the first attempt to
write it down.
