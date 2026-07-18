# Competitive Teardown — Tasks

> **Status**: `READY` — Spec complete, not yet executed (Phase 0.2 gate)

## Phase 1: Reproduce the commodity (expected to port)

- [ ] T1: Write `predict_linear` rules for C1 (queue_depth, hikaricp_pending,
      heap_growth) in existing `vmrules.yaml` format. Log effort (hours).
- [ ] T2: Configure Alertmanager `group_by: [workload]` reproducing C2.
      Confirm it collapses same alerts as workload-collapse code.
- [ ] T3: Build Robusta playbook reproducing C3+C4 (enrichment + deep-links)
      on a sample alert. Log what ported and what was lost.
- [ ] T4: Confirm C5 (dispatch is already Alertmanager — trivial, document).

## Phase 2: Attack the core candidate (the real test)

- [ ] T5: Pick ONE causal chain from `degradation-model.md` (suggested: .NET
      N1 threadpool→queue→latency→errors — best metric coverage).
- [ ] T6: Attempt to express it as Robusta playbook — can the playbook assert
      "queue filled BEFORE latency rose, therefore cause is threadpool, not
      dependency"? (depends on: T5)
- [ ] T7: Attempt to express it as recording rules in Prometheus — can temporal
      ordering (precedence) distinguishing N1 from N3 be encoded without
      reimplementing correlation? (depends on: T5)
- [ ] T8: Score T6/T7 results against PORTS/RESISTS criteria. (depends on: T6, T7)

## Phase 3: Decision

- [ ] T9: Consolidate C1-C6 matrix with verdict per capability (ported /
      resisted / partial) + evidence (the config artifact attempted).
      (depends on: T1-T4, T8)
- [ ] T10: Take experiment decision (product / not-product / minimum-product)
      per fixed criteria. (depends on: T9)
- [ ] T11: Update `docs/hypothesis-causal-origination.md` — gate 2 with
      verdict + evidence. (depends on: T10)
- [ ] T12: If "product": update `docs/architecture/decisions.md` promoting
      hypothesis to ADR with scope = fraction that resisted. If "not-product":
      close hypothesis as refuted, register shippable configs as the real
      deliverable. (depends on: T10)
- [ ] T13: Mark P0.2 in ROADMAP with result. (depends on: T10)
