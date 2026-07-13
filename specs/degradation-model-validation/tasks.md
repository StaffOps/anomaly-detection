# Tasks: Degradation Model Validation (P0.3)

> **Status**: `TODO` — Blocked on replay infra refinement and incident data identification

- [ ] T1: Identify 3+ historical incidents with known root cause in production data (last 30 days)
- [ ] T2: For each incident, run replay over the incident window (depends on: T1)
- [ ] T3: Map detected anomalies to degradation model chains — does the predicted leading indicator appear before the lagging? (depends on: T2)
- [ ] T4: Score each chain (confirmed / refuted / insufficient) with timestamps as evidence (depends on: T3)
- [ ] T5: Update docs/architecture/degradation-model.md with validation column per chain (depends on: T4)
- [ ] T6: Summarize findings — feed into P0.2 competitive teardown as input for C6 assessment (depends on: T4)
