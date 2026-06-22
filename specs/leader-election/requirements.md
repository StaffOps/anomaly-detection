# Feature: Leader Election (P5.1)

## Overview

K8s Lease-based leader election for the anomaly-detection controller, implemented in `internal/leader/` package. Wraps `k8s.io/client-go/tools/leaderelection` to ensure only one controller replica runs detection cycles at a time while followers stay warm for fast takeover.

## User Stories

### US-1: HA for SRE Operations

AS an SRE running multiple controller replicas in production,
WHEN the active leader pod is terminated or becomes unhealthy,
THEN the system SHALL elect a new leader within ~17s (LeaseDuration 15s + RetryPeriod 2s) and resume detection cycles without manual intervention.

### US-2: Platform Team HA Guarantee

AS a platform team member responsible for anomaly detection availability,
WHEN I deploy the controller with `replicas: 3`,
THEN the system SHALL guarantee exactly one active leader running detection cycles, with followers ready to take over, providing HA without split-brain.

### US-3: Local Development Simplicity

AS a developer running the controller locally via docker-compose,
WHEN `controller.leader_election.enabled` is `false` (default),
THEN the system SHALL run detection cycles unconditionally without requiring K8s API access.

## Functional Requirements

### Leader Election Mechanism

- Wraps `k8s.io/client-go/tools/leaderelection` with K8s Lease objects
- Configurable via `controller.leader_election.enabled` (default: `false`)
- Only the lease holder executes detection cycles; followers remain idle but warm
- Takeover time: ~17s (LeaseDuration 15s + RetryPeriod 2s)
- RenewDeadline: 10s

### Identity Resolution

- Defaults to `POD_NAME` environment variable (populated via K8s downward API)
- Falls back to `os.Hostname()` when `POD_NAME` is not set
- Identity must be unique per replica within the namespace

### Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `staffops_ad_controller_is_leader` | Gauge | 1 if this instance is leader, 0 otherwise |
| `staffops_ad_controller_leader_transitions_total` | Counter | Total number of leader transitions observed |

### RBAC

- Role grants `get`, `create`, `update` on `coordination.k8s.io/leases` resource
- Scoped to the controller's namespace only

### Testing

7 unit tests covering:
1. Validation error when lease name is empty
2. Validation error when lease namespace is empty
3. Validation error when identity is empty
4. Identity resolution from `POD_NAME` env var
5. Identity fallback to hostname when `POD_NAME` unset
6. In-cluster kubeconfig detection
7. Out-of-cluster kubeconfig fallback (KUBECONFIG path)

## Acceptance Criteria

- [ ] Leader election activates when `controller.leader_election.enabled: true`
- [ ] Only one replica runs detection cycles at any time (no split-brain)
- [ ] Takeover completes within ~17s of leader loss
- [ ] `staffops_ad_controller_is_leader` gauge is exposed and toggles correctly
- [ ] `staffops_ad_controller_leader_transitions_total` increments on transitions
- [ ] RBAC Role with `coordination.k8s.io/leases` permissions is defined
- [ ] All 7 unit tests pass
- [ ] When disabled (default), controller runs detection unconditionally

## Out of Scope

- Multi-cluster leader election (single cluster only)
- Lease-based work sharding (future — only leader/follower binary)
- Automatic replica scaling based on leader health
- Integration testing against real K8s cluster (covered in P5.2)
