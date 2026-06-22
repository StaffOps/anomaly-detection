# Feature: FDR Correction (P0.4)

## Context

P0.4 gate. ~400 adaptive series at fixed z>3 ≈ ~1000+ FP/day from multiple comparisons alone. Benjamini-Hochberg FDR control applied per detection cycle before dispatch.

Independent of which thesis wins (product or config) — this is the largest FP source and a generic statistical fix.

## User Stories

WHEN FDR is applied per detection cycle THEN on-call SREs receive significantly fewer false positives without losing true positives.

WHEN FDR reduces FPs THEN the project owner has evidence that the biggest noise source was statistical (not detector-specific), validating the multiple-comparisons hypothesis.

## Acceptance Criteria

- [ ] Benjamini-Hochberg applied per cycle across all adaptive z-score results
- [ ] Configurable FDR target (default 0.05 = 5% expected false discovery rate)
- [ ] Metric showing rejected vs accepted anomalies per cycle
- [ ] Replay comparison: before vs after FDR on same window (FP reduction quantified)
- [ ] No regression in detection of high-confidence anomalies (z>5 should always pass)

## Out of Scope

- Changing the detector algorithm
- ML integration
- Enrichment changes

## Dependencies

- Replay mode (for before/after comparison)
