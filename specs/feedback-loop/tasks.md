# Tasks: Feedback Loop (P3.3)

> **Status**: `FUTURE` — Blocked on P5.3 (controller out of dry-run, real alerts flowing)

- [ ] Task 1: Add config struct for feedback loop (Slack signing secret, channel ID, min_samples, min_threshold, auto-tune interval, report schedule)
- [ ] Task 2: Implement Slack webhook HTTP handler (`POST /api/v1/feedback`) — validate signing secret, parse `reaction_added` payload, extract alert fingerprint + verdict
- [ ] Task 3: Implement feedback store (Redis sorted set ops: store entry, query by rule+time range, count by verdict, TTL 30d on keys)
- [ ] Task 4: Implement precision/recall computation per rule over configurable time window
- [ ] Task 5: Implement auto-tune logic — periodic goroutine, check FP rate per rule, raise zscore_threshold if precision < threshold, respect min_samples and min_threshold floor
- [ ] Task 6: Implement weekly reporter — compute top-5 noisy rules + per-rule precision/recall, format Slack message, post via webhook
- [ ] Task 7: Expose Prometheus metrics: `feedback_total{rule,verdict}`, `feedback_precision_ratio{rule}`, `feedback_recall_ratio{rule}`, `threshold_adjustments_total{rule,direction}`
- [ ] Task 8: Add config validation and defaults for feedback loop settings
- [ ] Task 9: Write unit tests for feedback store (Redis mock), precision/recall math, auto-tune bounds logic
- [ ] Task 10: Write integration test — end-to-end: webhook → store → compute precision → verify threshold adjustment
- [ ] Task 11: Add Slack app setup documentation (Events API subscription, required scopes: `reactions:read`, signing secret config)
- [ ] Task 12: Update docker-compose to include feedback loop config and expose webhook port
