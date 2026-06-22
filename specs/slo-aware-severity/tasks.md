# Tasks: SLO-Aware Severity Adjustment (P3.4)

> **Status**: `FUTURE` — Blocked on P5.3 (real alerts) + SLO recording rules existing in cluster

- [ ] Task 1: Define SLO catalog config schema (YAML struct, validation, loading at startup)
- [ ] Task 2: Implement budget querier (instant PromQL query, timeout, error handling → passthrough)
- [ ] Task 3: Implement severity adjuster (threshold logic, one-step mutation, passthrough for unknowns)
- [ ] Task 4: Integrate adjuster into anomaly pipeline (after enrichment, before dispatch)
- [ ] Task 5: Add annotations to adjusted alerts (`slo_original_severity`, `slo_adjusted_severity`, `slo_budget_remaining_pct`)
- [ ] Task 6: Emit metrics (adjustments by direction, query latency histogram, query errors counter)
- [ ] Task 7: Write tests (unit: adjuster logic + edge cases; integration: pipeline with mock TSDB)
- [ ] Task 8: Document configuration (catalog format, thresholds, troubleshooting query failures)
