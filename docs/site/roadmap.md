# Roadmap

Ordered by priority — items higher up are nearer-term.

## Phase Overview

| Phase | Focus | Status |
|-------|-------|--------|
| 1 — Detection quality & UX | Enrichment, links, readiness | ✅ Done |
| 2 — ML maturity | Feature vectors, workload patterns, forecast | 🟡 Partial |
| 3 — Operational maturity | Replay mode, dashboards, feedback | 🚧 In progress |
| 4 — Observability hardening | Instrumentation fixes, cardinality | 🎯 Next |
| 5 — Cluster readiness | Leader election, deploy, real alerts | 🔜 Planned |
| 6 — Self-monitoring | VMRules, dashboards, OTel SDK | 🔜 Planned |

---

## ✅ Phase 1 — Detection Quality & UX (Done)

- **P1.1** — Label-based pivot (anomaly enrichment) with identity extraction, template substitution, bounded concurrency, Redis-backed cache
- **P1.2** — Alert payload with Grafana/Tempo/Loki/Runbook deep links anchored at anomaly timestamp
- **P1.3** — Complete readiness checks (`/readyz` probes VM, Loki, Alertmanager, ML)

## 🟡 Phase 2 — ML Maturity

- [x] **P2.1** — Fix ML multivariate (proper feature vector per correlated alert)
- [x] **P2.4** — Workload-aware correlation (sibling pod detection, ≥3 pods → workload alert)
- [ ] **P2.5** — ML feature: `replica_anomaly_fraction` (blocked on P2.4 prod validation)
- [ ] **P2.2** — Wire ML Forecast (Prophet) into detection cycle
- [ ] **P2.3** — Multivariate per-namespace mode

## 🚧 Phase 3 — Operational Maturity

- [x] **P3.1** — Replay mode (16/16 tasks done) — see [Replay Mode](operations/replay.md)
- [x] **P3.2** — Top noisy workloads dashboard / VMRule — see [Monitoring](operations/monitoring.md)
- [ ] **P3.3** — Feedback loop (Slack reactions → precision/recall tracking)
- [ ] **P3.4** — SLO-aware severity adjustment

## 🎯 Phase 4 — Observability Hardening (Prerequisite for Deploy)

- [x] **P4.A.1** — Fix instrumentation bugs — done in 0.7.0 (counter before dry-run, gauge on tick, custom histogram buckets)
- [x] **P4.A.2** — Cardinality cleanup — done in 0.7.0 (no `identity` label on any metric; bounded `workload` in AM labels)
- [x] **P4.A.3** — Multi-cluster constant labels — done in 0.7.0 (`WrapRegistererWith{cluster}`, eks_cluster at scrape layer)
- [ ] **P4.A.4** — Dashboard refresh

## 🔜 Phase 5 — Cluster Readiness

- [x] **P5.1** — K8s Lease leader election (`internal/leader/`, configurable via `controller.leader_election.enabled`)
- [ ] **P5.2** — Deploy to cluster (validate IRSA, ApplicationSet)
- [ ] **P5.3** — Remove `--dry-run`, validate real alerts
- [ ] **P5.4** — Cardinality guard (self-protection)
- [ ] **P5.5** — Agent API Integration (staffops-chaitops) — invoke Agent API on high-confidence anomalies for automated squad investigation; circuit breaker, bounded concurrency (max 5), Redis dedup. Blocked on P5.3. [Spec](../specs/agent-api-integration/)

## 🔜 Phase 6 — Self-Monitoring

- [ ] **P6.1** — VMRules for `staffops_ad_*` metrics
- [ ] **P6.2** — Grafana dashboard refresh
- [ ] **P6.3** — OTel SDK adoption (traceID-correlated logs)
