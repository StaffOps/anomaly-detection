# Design: Leader Election (P5.1)

## Architecture

```
┌─────────────────────────────────────────────────┐
│ Controller Pod (replica N)                      │
│                                                 │
│  ┌──────────────┐    ┌───────────────────────┐  │
│  │ internal/    │    │ Detection Loop        │  │
│  │ leader/      │───▶│ (runs only if leader) │  │
│  │              │    └───────────────────────┘  │
│  │ - Manager    │                               │
│  │ - Metrics    │    ┌───────────────────────┐  │
│  │ - Config     │───▶│ Prometheus /metrics    │  │
│  └──────┬───────┘    └───────────────────────┘  │
│         │                                       │
└─────────┼───────────────────────────────────────┘
          │ Lease renew/acquire
          ▼
┌─────────────────────┐
│ K8s API Server      │
│ coordination.k8s.io │
│ /leases             │
└─────────────────────┘
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `internal/leader/leader.go` | Manager struct wrapping `leaderelection.LeaderElector` |
| `internal/leader/config.go` | Config validation and identity resolution |
| `internal/leader/metrics.go` | Prometheus gauge + counter registration |
| `internal/leader/leader_test.go` | 7 unit tests |

## Rationale

### Decision 1: K8s Lease over Redis lock

**Choice**: Use native `coordination.k8s.io/leases` via `client-go/tools/leaderelection`.

**Justification, in order of strength**:
1. No extra dependency — Redis is already used for baselines but adding lock semantics to it creates coupling between data store and coordination
2. Native K8s primitive with battle-tested semantics; works with existing RBAC and ServiceAccount
3. Standard pattern used by controller-runtime, kube-scheduler, and kube-controller-manager

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Requires K8s API access | Controller already runs in-cluster with ServiceAccount |
| Not usable outside K8s | Local dev disables it (config toggle) |

**When this decision would be wrong**:
- Controller needs to run outside K8s permanently (not planned)
- Lease API becomes a bottleneck (unlikely at 1 lease per controller)

### Decision 2: Configurable, default off

**Choice**: `controller.leader_election.enabled` defaults to `false`.

**Justification, in order of strength**:
1. Local dev (docker-compose) has no K8s API — leader election would fail or require mocking
2. Single-instance mode is simpler to debug and develop against
3. Zero-config local experience; production explicitly opts in via Helm values

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Production must explicitly enable | Helm values template handles this; impossible to forget in CD |

**When this decision would be wrong**:
- If local dev commonly runs multiple replicas (not the case today)

### Decision 3: ~17s takeover window

**Choice**: LeaseDuration 15s, RenewDeadline 10s, RetryPeriod 2s.

**Justification, in order of strength**:
1. LeaseDuration 15s is the standard used by kube-controller-manager — well-understood failure semantics
2. Balances fast failover (~17s worst case) against lease churn (shorter durations cause more API calls)
3. Detection cycles run every 30-60s — missing one cycle during failover is acceptable

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Up to 17s gap in detection | Detection interval is 30-60s; one missed cycle has negligible impact on alerting SLO |
| Lease renewal adds API load | 1 PUT every ~5s (RetryPeriod) per leader — trivial |

**When this decision would be wrong**:
- Real-time alerting requiring <5s failover (not our use case)

### Decision 4: POD_NAME as identity

**Choice**: Use `POD_NAME` env var (downward API), fallback to `os.Hostname()`.

**Justification, in order of strength**:
1. Unique per replica — K8s guarantees pod name uniqueness within namespace
2. Survives container restarts within same pod (identity doesn't change)
3. Visible in `kubectl get lease` output — easy debugging

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Requires downward API in pod spec | Standard K8s pattern; one env var in Helm template |

**When this decision would be wrong**:
- If multiple controllers share the same pod (not architecturally possible)

## Invariants

- Exactly one leader at any time (enforced by K8s Lease API atomicity)
- Followers NEVER run detection cycles
- Leader loss is detected within LeaseDuration (15s)
- Metrics always reflect current state (no stale gauge)

## Dependencies

| Service | Purpose |
|---------|---------|
| K8s API Server | Lease object CRUD |
| `k8s.io/client-go` | LeaderElector implementation |
| Prometheus client_golang | Metrics exposition |
