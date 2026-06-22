# Tasks: FDR Correction (P0.4)

> **Status**: `TODO` — Ready to implement, no external blockers

- [ ] T1: Add p-value computation from z-score in `internal/detection/adaptive.go`
- [ ] T2: Implement Benjamini-Hochberg procedure in `internal/detection/fdr.go` (depends on: T1)
- [ ] T3: Wire FDR into detection pipeline: filter anomalies post-adaptive, pre-correlation (depends on: T2)
- [ ] T4: Add config field `controller.fdr_target` (default 0.05) with hot-reload (depends on: T3)
- [ ] T5: Add metrics: `staffops_ad_detection_fdr_rejected_total`, `staffops_ad_detection_fdr_accepted_total` (depends on: T3)
- [ ] T6: Replay comparison: run same window with and without FDR, quantify FP reduction (depends on: T3)
- [ ] T7: Validate no regression on high-z anomalies (z>5 always passes BH at any reasonable FDR) (depends on: T6)
