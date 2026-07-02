# Troubleshooting

## Common Issues

### No anomalies detected

**Symptoms**: `staffops_ad_detection_anomalies_total` stays at 0.

**Causes:**

1. **Baselines still warming up** — wait 30+ minutes (60 samples × 30s)
2. **All namespaces suppressed** — check `EXCLUDE_NAMESPACES_CSV`
3. **Queries returning empty** — verify VM_URL/LOKI_URL are correct and have data

**Diagnosis:**

```bash
# Check if queries are executing
curl -s localhost:8080/metrics | grep worker_queries_total

# Check if baselines are being tracked
curl -s localhost:8080/metrics | grep baseline_series_tracked

# Check controller logs for query errors
docker compose -f scripts/docker-compose.yaml logs controller | grep -i error
```

---

### `/readyz` returns 503

**Symptoms**: Health check failing, one or more dependencies unreachable.

**Diagnosis:**

```bash
# Check which dependency is failing
curl -s localhost:8080/metrics | grep readiness_checks_total
# Look for result="error"
```

**Common fixes:**

| Dependency | Fix |
|------------|-----|
| Redis | Check `REDIS_ADDR`, verify Redis is running |
| VictoriaMetrics | Check `VM_URL`, verify network access from container |
| Loki | Check `LOKI_URL`, verify network access |
| Alertmanager | Check `ALERTMANAGER_URL`, may be transient (flapping) |

!!! note "Transient failures"
    The readiness check does not have retry/threshold logic. A single transient failure causes 503. This is a known limitation — anti-flapping logic is planned.

---

### ML errors (feature count mismatch)

**Symptoms**: `staffops_ad_ml_calls_total{status="error"}` increasing.

**Cause**: Isolation Forest model fitted with N features, then receives request with different N.

**Root cause**: Pod-level enrichment produces 6+ features, service-level produces 3-5. Single model can't handle both.

**Workaround**: This is a known bug. ML errors are non-blocking — detection continues without ML confirmation.

**Fix planned**: Separate models per kind (pod vs service).

---

### High cycle duration (> 15s)

**Symptoms**: `staffops_ad_controller_cycle_duration_seconds` p99 > 15s.

**Causes:**

1. Too many queries per cycle (many rules × many workloads)
2. Slow VM/Loki responses
3. Enrichment queries adding latency (5-7 queries per alert)

**Mitigation:**

- Increase `controller.job_interval` (e.g., 60s instead of 30s)
- Reduce number of adaptive metrics
- Add more workers (scale query throughput)
- Check VM/Loki latency independently

---

### Alerts firing for infrastructure workloads

**Symptoms**: Alerts for `vmselect`, `ztunnel`, `istio-cni`, `fluent-bit`.

**Cause**: Infrastructure pods have variable workload that triggers adaptive detection.

**Fix**: Add their namespaces to `EXCLUDE_STATIC_ONLY_CSV`:

```bash
EXCLUDE_STATIC_ONLY_CSV=monitoring,istio-system,kube-system
```

This suppresses static rules but keeps adaptive for true anomalies.

---

### Service-level anomalies collapsing into single alert

**Symptoms**: Multiple distinct services appear as one alert with empty namespace/pod.

**Cause**: Known bug — service-level metrics have `service_name` but no `namespace`/`pod`. The correlator uses `namespace/pod` as workload key, resulting in all services mapping to `"/"`.

**Status**: Fix planned — use `service_name` as fallback in `workloadKey()`.

---

## Diagnostic Commands

```bash
# Stack status
docker compose -f scripts/docker-compose.yaml ps

# Controller logs (last 50 lines)
docker compose -f scripts/docker-compose.yaml logs controller --tail 50

# ML service logs
docker compose -f scripts/docker-compose.yaml logs ml --tail 50

# Redis state (baseline count)
docker compose -f scripts/docker-compose.yaml exec redis redis-cli DBSIZE

# Redis dedup keys
docker compose -f scripts/docker-compose.yaml exec redis redis-cli KEYS "dedup:*"

# Metrics endpoint
curl -s localhost:8080/metrics | grep staffops_ad

# Health check
curl -s localhost:8080/readyz
```

## Log Levels

Controller uses structured logging (`slog`). Key log fields:

| Field | Meaning |
|-------|---------|
| `mode=replay` | Running in replay mode |
| `cycle=N` | Detection cycle number |
| `anomalies=N` | Anomalies found this cycle |
| `detector=...` | Which detector fired |
| `dry_run=true` | Alert not actually dispatched |
