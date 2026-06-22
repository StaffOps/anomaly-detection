# Competitive Teardown — Design

## Approach

**Invalidation-first**: attempt to reproduce each system capability in
incumbent tooling. What ports cheaply was never a product — it was config.
What resists is the empirically-found core.

## Capability inventory

| # | Capability | Incumbent to test against |
|---|-----------|--------------------------|
| C1 | Saturation toward ceiling (predict breach) | `predict_linear` in vmrules.yaml |
| C2 | Workload-collapse (≥3 siblings → 1 alert) | Alertmanager `group_by: [workload]` |
| C3 | Alert enrichment (cpu/mem/error/latency ratios) | Robusta playbook |
| C4 | Deep-links (Grafana/Tempo/Loki/runbook) | Robusta playbook / annotation templates |
| C5 | Dedup / dispatch | Alertmanager (already the backend) |
| C6 | **Intra-runtime causal chain** (.NET threadpool→queue→latency→errors) | Robusta playbook? Recording rules? |

C1-C5 are expected to port (confirming "commodity"). **C6 is the only
candidate to resist** — and if it resists, it IS the product.

## Decision criteria (fixed a priori)

### A capability PORTS if:

- Reproducible in ≤ ~1 day of config work, AND
- No essential property lost (operator receives same value), AND
- No custom code beyond declarative templates/queries required.

### A capability RESISTS if:

- Reproducing it requires encoding **causal logic** (order, precedence,
  "X precedes Y"), not just aggregation/threshold/template, AND
- No incumbent playbook/rule expresses that logic without effectively
  reimplementing what we're evaluating.

### Final decision:

| Outcome | Action |
|---------|--------|
| C1-C5 port, C6 resists | Product = causal intra-runtime layer. Ship C1-C5 as config. |
| C6 also ports | **No product.** Ship rules + playbook, close hypothesis. |
| C6 ambiguous (partial port) | Name the fraction that resists; that fraction = minimum product. |

## Rationale

### Decision: Test by invalidation, not validation

**Choice**: try to kill the hypothesis before building on it.

**Justification**:
1. Cheapest possible test (days, not weeks)
2. Eliminates confirmation bias (we want it to fail if it should)
3. Produces shippable artifacts even in the "no product" outcome

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Robusta may not be available for real test | Document as "documental analysis" — weaker verdict |
| Experiment doesn't prove the product works, only that it's needed | P0.1 (measurement) complements this |

**When this decision would be wrong**:
- If the cost of the experiment exceeds the cost of just building (unlikely — days vs months)

## Dependencies

- `degradation-model.md` written (✅ exists)
- Robusta availability for real test (to confirm — if unavailable, documental analysis)
- Ideally runs after or parallel to P0.1 (measurement informs whether C1-C5 matter)

## Invariants

- Criteria PORTS/RESISTS are fixed BEFORE running (no post-hoc rationalization)
- Each verdict includes the actual artifact attempted (not just opinion)
- Effort logged in hours per capability
