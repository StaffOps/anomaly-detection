# Design: Feedback Loop (P3.3)

## Architecture

```
Slack (reaction_added event)
  │
  ▼
Slack Events API → HTTP POST webhook
  │
  ▼
Controller /api/v1/feedback endpoint
  │
  ▼
Redis sorted sets (per rule, TTL 30d)
  │
  ├──▶ Auto-tune goroutine (periodic, e.g. every 6h)
  │      └── adjusts zscore_threshold in config
  │
  └──▶ Weekly reporter goroutine (cron: Monday 09:00)
         └── posts summary to Slack channel
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `feedback_handler.go` | HTTP endpoint receiving Slack webhook, validates signature, extracts alert fingerprint + verdict |
| `feedback_store.go` | Redis sorted set operations: store, query by time range, TTL management |
| `precision_recall.go` | Computes precision/recall per rule from stored feedback |
| `auto_tune.go` | Periodic threshold adjustment with safety bounds |
| `weekly_reporter.go` | Generates and posts weekly noise report to Slack |

## Rationale

### Decision 1: Redis sorted sets for feedback storage

**Choice**: Store feedback in Redis sorted sets (one key per rule), score=Unix timestamp, member=`{fingerprint}:{tp|fp}`.

**Justification**:
1. Redis already deployed in the stack — zero new infrastructure
2. TTL 30d handles retention automatically (no cleanup job)
3. Sorted sets support efficient time-range queries (`ZRANGEBYSCORE`) for windowed precision/recall
4. Feedback volume is low (~tens/day) — Redis handles trivially

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| No durability guarantees (Redis restart = data loss) | Acceptable — feedback is supplementary signal, not critical state. Rebuilds naturally over 30d. |
| No complex queries (joins, aggregations) | Precision/recall computation is simple arithmetic over a small set |

**When wrong**: If feedback volume exceeds ~10k/day or we need historical analysis beyond 30d → migrate to PostgreSQL/TimescaleDB.

### Decision 2: Conservative auto-tune (raise-only)

**Choice**: Auto-tune only raises `zscore_threshold` (tightens detection). Never lowers.

**Justification**:
1. Lowering thresholds risks alert storms — one bad auto-tune could overwhelm on-call
2. Raising is always safe (fewer alerts, possibly missing real anomalies but not flooding)
3. Manual intervention required to lower — keeps human in the loop for "open the floodgates" decisions

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| System can't self-recover if threshold was raised too high | Human reviews weekly report, manually lowers if recall drops |
| Requires min N samples → slow to act | Intentional — sparse data shouldn't trigger threshold changes |

**When wrong**: If feedback becomes dense (>50 samples/rule/week) AND operators trust the system → consider bidirectional tuning with rate limits.

### Decision 3: Slack Events API (not custom bot)

**Choice**: Use Slack Events API `reaction_added` subscription → webhook to controller.

**Justification**:
1. No custom bot infrastructure needed (Slack app with Events API scope is sufficient)
2. Reactions are lightweight UX — SRE doesn't leave the alert thread
3. Events API is push-based (no polling)

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Must filter reactions to only anomaly-detection alert messages | Filter by bot_id or message metadata in handler |
| Slack API rate limits (not a concern at our volume) | <100 reactions/day expected |

### Decision 4: Precision/recall as lower bounds

**Choice**: Accept that precision/recall estimates are lower bounds because not every alert gets feedback.

**Justification**:
1. Forcing feedback on every alert degrades SRE experience
2. Alerts without feedback are excluded from computation (not counted as TP or FP)
3. Weekly report clearly labels metrics as "based on N rated alerts out of M total"

## Invariants

- `zscore_threshold` MUST NOT go below `min_threshold` (safety floor, default 2.0)
- Auto-tune MUST NOT act with fewer than `min_samples` feedback entries (default 20)
- Feedback entries MUST expire after 30d (Redis TTL)
- Webhook endpoint MUST validate Slack signing secret before processing

## Dependencies

| Service | Purpose |
|---------|---------|
| Redis | Feedback storage (sorted sets) |
| Slack Events API | `reaction_added` event delivery |
| Slack Incoming Webhook | Weekly report posting |
| Alertmanager | Source of alert fingerprints in Slack messages |
