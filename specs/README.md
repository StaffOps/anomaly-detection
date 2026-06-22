# Specs Index

Feature and experiment specifications for `staffops-anomaly-detection`.

## Status legend

| Status | Meaning |
|--------|---------|
| `DONE` | Implemented and validated |
| `IN-PROGRESS` | Actively being worked on |
| `READY` | Spec complete, not yet started |
| `BLOCKED` | Waiting on external dependency or prerequisite |
| `TODO` | Spec written, ready to implement |
| `FUTURE` | Planned but not prioritized yet |

## Specs

### Completed (historical traceability)

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [detection-core](detection-core/) | `DONE` | P1 | Enrichment, alert links, readiness checks |
| [ml-maturity](ml-maturity/) | `DONE` | P2 | Multivariate fix, workload-aware correlation |
| [observability-hardening](observability-hardening/) | `DONE` | P4 | Instrumentation bugs, cardinality, labels, OTel SDK |
| [leader-election](leader-election/) | `DONE` | P5.1 | K8s Lease-based HA for controller |
| [replay-mode](replay-mode/) | `DONE` | P3.1 | Offline replay of historical data for testing detection |
| [ci-cd-pipeline](ci-cd-pipeline/) | `DONE` | — | GitHub Actions: test, build, release, SAST, docs |

### Active / Next

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [production-hardening](production-hardening/) | `IN-PROGRESS` | P5 | Kyverno admission, CI gates, Helm chart, GitOps |
| [fdr-correction](fdr-correction/) | `TODO` | P0.4 | Benjamini-Hochberg FDR to cut ~1000+ FP/day |
| [degradation-model-validation](degradation-model-validation/) | `TODO` | P0.3 | Validate causal chains against real incidents |
| [synthetic-injection](synthetic-injection/) | `READY` | P0.1 | Inject synthetic faults to measure recall/FP bounds |
| [competitive-teardown](competitive-teardown/) | `READY` | P0.2 | Time-boxed: can the value be reproduced as config? |

### Future

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [falco-integration](falco-integration/) | `BLOCKED` | P2.7 | Runtime security signal as 4th ingestion source |
| [agent-api-integration](agent-api-integration/) | `FUTURE` | P5.5 | Trigger AI agent investigation on high-confidence alerts |

## Relationship to ROADMAP.md

Specs cover **scoped work items** with requirements, design, and tasks.
`ROADMAP.md` is the higher-level strategic view with phases and priorities.

### Items in ROADMAP without dedicated spec (too small or future)

- P2.2 Wire ML Forecast (Prophet) — medium, no spec yet
- P2.3 Multivariate per-namespace — medium, no spec yet
- P2.5 ML feature: replica_anomaly_fraction — small, blocked on P2.4
- P2.8 Workload-identity baseline keying — medium
- P2.9 Outlier rejection (anti-poisoning) — small-medium
- P2.10 Absence-of-signal detection — medium
- P3.3 Feedback loop — large, candidate for spec
- P3.4 SLO-aware severity — medium
- P5.4 Cardinality guard — small
- P6.1 Self-monitoring VMRules — small

## Project direction (as of 2026-06-22)

```
Phase 0 gates (BLOCKING — strategic decision)
│
├── P0.1 Synthetic injection → measures detector quality
├── P0.2 Competitive teardown → decides if product exists
├── P0.3 Degradation model validation → grounds the hypothesis
└── P0.4 FDR correction → cuts largest FP source (independent)
│
├── IF product exists → build causal origination layer (C6)
└── IF not → ship config (vmrules + playbooks), close project
│
Phase 5 (PARALLEL with Phase 0 — no conflict)
└── Production hardening → unblocks cluster deploy
```

The project is at a **strategic inflection point**: Phase 0 decides whether
there's a product to build or just config to ship. Phase 5 (production
hardening) proceeds in parallel since it's needed either way.
