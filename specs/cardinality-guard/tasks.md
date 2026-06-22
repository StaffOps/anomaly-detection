# Tasks: Cardinality Guard

> **Status**: `TODO` — Small, no external blockers

- [ ] Task 1: Add `max_baseline_series` config field (default 10000) to worker config struct + YAML
- [ ] Task 2: Implement threshold check in baseline store — reject new key creation when count ≥ limit (depends on: Task 1)
- [ ] Task 3: Expose `staffops_ad_worker_baseline_series_tracked` gauge metric from baseline store
- [ ] Task 4: Add alert annotation/rule that fires when metric ≥ threshold (depends on: Task 3)
- [ ] Task 5: Unit tests — threshold enforcement, metric accuracy, existing baselines unaffected (depends on: Task 2, Task 3)
