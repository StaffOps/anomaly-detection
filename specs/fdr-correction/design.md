# Design: FDR Correction (P0.4)

## Algorithm

Benjamini-Hochberg procedure on p-values derived from z-scores, applied per detection cycle.

1. Collect all adaptive z-score results for the cycle (N ≈ 400 series)
2. Convert each |z| to a two-tailed p-value: `p = 2 * (1 - Φ(|z|))`
3. Sort p-values ascending: p(1) ≤ p(2) ≤ ... ≤ p(N)
4. Find largest k where `p(k) ≤ (k/N) * FDR_target`
5. Reject (accept as anomaly) all series with p(i) ≤ p(k)

## Where in Pipeline

```
adaptive detection → [FDR filter] → correlation → dispatch
```

After adaptive detection produces z-scores, before correlation/dispatch consumes them.

## Rationale

### Why BH over Bonferroni?

**Choice**: Benjamini-Hochberg (FDR control) instead of Bonferroni (FWER control).

**Justification**:
1. With N=400, Bonferroni threshold becomes z > 4.4 — too conservative, kills real anomalies
2. BH controls the *proportion* of false discoveries, not the probability of *any* false discovery — appropriate for monitoring where a few FPs are tolerable
3. BH has more statistical power (detects more true anomalies) at the same error budget

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Some FPs still pass (up to 5% of accepted) | Acceptable — downstream correlation further filters |
| More complex than simple threshold | Single function, well-understood algorithm |

### Why per-cycle?

Each detection cycle is an independent multiple-testing event. The family size is the number of series evaluated in that cycle. Cross-cycle independence means no state accumulation needed.

### Trade-off: weak signals lost

Very marginal anomalies (z=3.01) will be rejected by BH when many series are tested. Genuine but weak signals are lost. This is acceptable because:
- z=3.01 has low confidence anyway (p≈0.003, but with 400 tests, expected ~1.2 by chance)
- High-confidence anomalies (z>5) always pass BH at FDR=0.05 (p < 5.7e-7, always below threshold)

**When this decision would be wrong** (signals to reopen):
- Recall drops on known incidents in replay → lower the FDR target (e.g., 0.10)
- Validated causal chains (P0.3) show leading indicators consistently at z=3-4 → need lower threshold for those specific series

## Invariants

- z > 5 anomalies MUST always pass FDR at any configured target ≤ 0.10
- FDR filter is stateless across cycles (no memory between runs)
- Config change (fdr_target) takes effect on next cycle (hot-reload)

## Dependencies

| Service | Purpose |
|---------|---------|
| VictoriaMetrics (replay) | Before/after comparison on same data window |
