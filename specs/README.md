# Specs Index

Feature and experiment specifications for `staffops-anomaly-detection`.

## Status legend

| Status | Meaning |
|--------|---------|
| `DONE` | Implemented and validated |
| `IN-PROGRESS` | Actively being worked on |
| `READY` | Spec complete, not yet started |
| `BLOCKED` | Waiting on external dependency or prerequisite |
| `FUTURE` | Planned but not prioritized yet |

## Specs

| Spec | Status | Phase | Summary |
|------|--------|-------|---------|
| [replay-mode](replay-mode/) | `DONE` | — | Offline replay of historical data for testing detection rules |
| [production-hardening](production-hardening/) | `IN-PROGRESS` | P5 | Kyverno admission, CI gates, Helm chart, GitOps, network policies |
| [synthetic-injection](synthetic-injection/) | `READY` | P0.1 | Inject synthetic faults over replay to measure recall/FP bounds |
| [competitive-teardown](competitive-teardown/) | `READY` | P0.2 | Time-boxed experiment: can the value be reproduced as config? |
| [falco-integration](falco-integration/) | `BLOCKED` | P2.7 | Runtime security signal (Falco) as 4th ingestion source |
| [agent-api-integration](agent-api-integration/) | `FUTURE` | — | Trigger AI agent investigation on high-confidence alerts |

## Relationship to ROADMAP.md

Specs cover **scoped work items** with requirements, design, and tasks.
`ROADMAP.md` is the higher-level strategic view with phases and priorities.
Not every roadmap item needs a spec — only those large enough to warrant
separate requirements/design/tasks tracking.
