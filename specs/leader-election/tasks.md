# Tasks: Leader Election (P5.1)

> **Status**: `DONE` — Implemented in controller 0.7.0. Cluster integration validation pending in P5.2.

- [x] Task 1: Create `internal/leader/config.go` with Config struct and validation (lease name, namespace, identity required)
- [x] Task 2: Implement identity resolution — `POD_NAME` env var with `os.Hostname()` fallback
- [x] Task 3: Implement kubeconfig resolution — in-cluster detection with out-of-cluster fallback via `KUBECONFIG`
- [x] Task 4: Create `internal/leader/metrics.go` — register `staffops_ad_controller_is_leader` gauge and `staffops_ad_controller_leader_transitions_total` counter
- [x] Task 5: Create `internal/leader/leader.go` — Manager struct wrapping `leaderelection.LeaderElector` with OnStartedLeading/OnStoppedLeading/OnNewLeader callbacks
- [x] Task 6: Integrate leader election into controller main loop — gate detection cycles behind `IsLeader()` check when enabled
- [x] Task 7: Write 7 unit tests in `internal/leader/leader_test.go` covering validation errors, identity resolution, kubeconfig handling
- [x] Task 8: Add `controller.leader_election` config section to `config.yaml` (enabled: false default)
- [x] Task 9: Define RBAC Role for `coordination.k8s.io/leases` (get/create/update) in Helm chart
