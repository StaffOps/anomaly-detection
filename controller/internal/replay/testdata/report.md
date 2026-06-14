# Replay Report

**Ran at**: 2026-05-30T20:00:00Z  
**Controller**: 0.7.0  
**Status**: anomalies_detected  

## Window

| Field | Value |
|-------|-------|
| Start | 2026-05-29T20:00:00Z |
| End | 2026-05-30T20:00:00Z |
| Warmup end | 2026-05-30T00:48:00Z |
| Warmup fraction | 0.20 |
| Tick interval | 30s |

## Totals

| Metric | Count |
|--------|-------|
| Anomalies | 3 |
| Warmup skipped | 0 |
| Query errors | 0 |

### By Severity

| Severity | Count |
|----------|-------|
| critical | 1 |
| warning | 2 |

### By Detector

| Detector | Count |
|----------|-------|
| adaptive | 2 |
| static | 1 |

### By Signal

| Signal | Count |
|--------|-------|
| logs | 1 |
| metrics | 2 |

### By Kind

| Kind | Count |
|------|-------|
| pod | 2 |
| workload | 1 |

## Top Workloads

| # | Namespace | Workload | Count |
|---|-----------|----------|-------|
| 1 | payments | pay-api | 2 |
| 2 | orders | order-svc | 1 |

## Timeline

```
███
```

| Hour (UTC) | Anomalies | Severity |
|------------|-----------|----------|
| 2026-05-30 12:00 | 1 | warning:1 |
| 2026-05-30 13:00 | 1 | critical:1 |
| 2026-05-30 14:00 | 1 | warning:1 |

## Execution

| Metric | Value |
|--------|-------|
| Duration | 187.4s |
| Ticks processed | 2304 |
| Ticks skipped | 2 |
| VM queries | 4608 |
| VM p95 latency | 0.420s |
| Loki queries | 1152 |
| Memory peak | 348.5 MB |

