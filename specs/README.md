# Specs Index

Feature and experiment specifications for `staffops-anomaly-detection`.

## Status legend

| Status | Meaning |
|--------|---------|
| `DONE` | Implemented and validated |
| `IN-PROGRESS` | Actively being worked on |
| `READY` | Spec complete, not yet started |
| `TODO` | Ready to implement, no blockers |
| `BLOCKED` | Waiting on external dependency or prerequisite |
| `FUTURE` | Planned but not prioritized yet |

## Specs

### Completed (historical traceability)

| Spec | Phase | Summary |
|------|-------|---------|
| [detection-core](detection-core/) | P1 | Enrichment, alert links, readiness checks |
| [ml-maturity](ml-maturity/) | P2 | Multivariate fix, workload-aware correlation |
| [observability-hardening](observability-hardening/) | P4 | Instrumentation bugs, cardinality, labels, OTel SDK |
| [leader-election](leader-election/) | P5.1 | K8s Lease-based HA for controller |
| [replay-mode](replay-mode/) | P3.1 | Offline replay of historical data for testing detection |
| [ci-cd-pipeline](ci-cd-pipeline/) | — | GitHub Actions: test, build, release, SAST, docs |

### Active / Next

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [production-hardening](production-hardening/) | `IN-PROGRESS` | P5 | Kyverno admission, CI gates, Helm chart, GitOps |
| [fdr-correction](fdr-correction/) | `TODO` | P0.4 | Benjamini-Hochberg FDR to cut ~1000+ FP/day |
| [baseline-robustness](baseline-robustness/) | `TODO` | P2.8-10 | Workload keying, anti-poisoning, dead man's switch |
| [cardinality-guard](cardinality-guard/) | `TODO` | P5.4 | Self-protection: cap baseline series count |
| [self-monitoring-rules](self-monitoring-rules/) | `TODO` | P6.1 | PrometheusRule/VMRule for system self-health |
| [degradation-model-validation](degradation-model-validation/) | `TODO` | P0.3 | Validate causal chains against real incidents |
| [synthetic-injection](synthetic-injection/) | `READY` | P0.1 | Inject synthetic faults to measure recall/FP bounds |
| [competitive-teardown](competitive-teardown/) | `READY` | P0.2 | Time-boxed: can the value be reproduced as config? |

### Future

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [service-dependency-graph](service-dependency-graph/) | `FUTURE` | P2.6 | Node graph: service dependencies, propagation, Grafana viz |
| [ml-forecast](ml-forecast/) | `FUTURE` | P2.2 | Wire Prophet forecasting for proactive breach alerts |
| [multivariate-namespace](multivariate-namespace/) | `FUTURE` | P2.3 | Namespace-wide anomaly → shared-dependency detection |
| [slo-aware-severity](slo-aware-severity/) | `FUTURE` | P3.4 | Dynamic severity based on SLO error budget |
| [feedback-loop](feedback-loop/) | `FUTURE` | P3.3 | Slack reactions → precision/recall → auto-tune thresholds |
| [falco-integration](falco-integration/) | `BLOCKED` | P2.7 | Runtime security signal as 4th ingestion source |
| [agent-api-integration](agent-api-integration/) | `FUTURE` | P5.5 | Trigger AI agent investigation on high-confidence alerts |

## Coverage

**21 specs total** covering every significant ROADMAP item.

### Items intentionally without spec (too small for own spec)

- P2.5 ML feature: `replica_anomaly_fraction` — 1 task, lives as future task in `ml-maturity`

## Relationship to ROADMAP.md

Specs cover **scoped work items** with requirements, design, and tasks.
`ROADMAP.md` is the higher-level strategic view with phases and priorities.

## Project direction (as of 2026-06-22)

```
Phase 0 gates (BLOCKING — strategic decision)
│
├── P0.1 Synthetic injection → measures detector quality
├── P0.2 Competitive teardown → decides if product exists
├── P0.3 Degradation model validation → grounds the hypothesis
└── P0.4 FDR correction → cuts largest FP source (independent)
│
├── IF product exists → build causal origination layer
│   ├── service-dependency-graph (inter-service)
│   ├── ml-forecast (proactive)
│   ├── multivariate-namespace (shared-dependency)
│   ├── slo-aware-severity (context-aware)
│   └── feedback-loop (self-improving)
│
└── IF not → ship config (vmrules + playbooks), close
│
Phase 5 (PARALLEL with Phase 0 — no conflict)
├── production-hardening → unblocks cluster deploy
├── baseline-robustness → reliability prerequisite
├── cardinality-guard → safety prerequisite
└── self-monitoring-rules → observability of observability
```
