# Production Hardening — Tasks

> **Status**: `IN-PROGRESS` — CI workflows done, test coverage + Helm chart WIP

Each task carries a `PH.N` identifier matching the corresponding entry in
`ROADMAP.md` → Phase 5 Pre-Reqs. Effort sizing: **S** ≤ 4h, **M** ≤ 1d, **L** ≤
3d. Tasks within the same group can run in parallel; groups have soft ordering.

## Group A — Kyverno admission (hard-fails) [parallelizable]

- [x] **PH.1** (done in chart PH.15) — Add `securityContext` to all four pod types (controller, worker,
  redis, ML). Required keys: `runAsNonRoot: true`, `runAsUser: 65534`,
  `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`,
  `capabilities.drop: ["ALL"]`. Validate by running a `kubectl apply --dry-run=server`
  through the dev-cluster Kyverno engine. **Effort: S** (4 manifests, mechanical).

- [ ] **PH.2** — Replace `:latest` and `REPLACE_ME_REGISTRY` placeholders.
  Files: `controller/deploy/controller.yaml` L57, `controller/deploy/worker.yaml`
  L52. Tag pattern: `<harbor>/staffops/anomaly-{controller,worker,ml}:<git-sha>`.
  Wire to `.github/workflows/ci.yml` (PH.12) so CI sets the tag. **Effort: S**.

- [ ] **PH.3** — Migrate base images to BDC golden (apko + cosign).
  - `controller/Dockerfile` L1: `golang:1.25-alpine` → `harbor.<org>/golden/golang-1.25`
  - `controller/Dockerfile` L10, L15: `alpine:3.20` → golden minimal
  - `ml/Dockerfile` L1: `python:3.11-slim` → `harbor.<org>/golden/python-3.11`
  - Validate cosign chain via `cosign verify` against the golden image catalog.
  - **Effort: M** (image work + CI verification).

- [x] **PH.4** (DONE 2026-07-02, canonical chart) — Enable Redis AUTH with file-mounted secret.
  - Set `requirepass` in Redis config (sourced from secret).
  - Add `ExternalSecret` CRD: `redis-password` from AWS Secrets Manager.
  - Mount as file (`/etc/secrets/redis-password`); update controller/worker
    config loader to read `REDIS_PASSWORD_FILE` instead of `REDIS_PASSWORD`.
  - Update `.env.example` to document the file-mount pattern.
  - **Effort: M** (Redis config + ESO wiring + loader change + tests).

- [ ] **PH.5** — Multi-stage `ml/Dockerfile`, drop `gcc`/`g++` from runtime.
  - Stage 1 (builder): install gcc, g++, build wheels for prophet/scikit-learn.
  - Stage 2 (runtime): copy wheels only, `pip install --no-deps` from the wheels.
  - Verify final image with `docker run --rm <image> which gcc` returns nothing.
  - **Effort: S**.

- [x] **PH.6** (done in chart PH.15) — Add mandatory labels to all pod templates.
  - `app.kubernetes.io/name`, `app.kubernetes.io/version` (from CI tag),
    `CostCenter` (from Helm values), `Environment` (from Helm values).
  - Apply as part of Helm chart in PH.15 — coordinate ordering.
  - **Effort: S**.

- [x] **PH.7** (done in chart PH.15) — Add `lifecycle.preStop` (`sleep 5`) and
  `terminationGracePeriodSeconds: 30` to controller, worker, ML, redis.
  Validate by killing a pod under load and confirming no in-flight gRPC errors.
  **Effort: S**.

- [x] **PH.8** (done in chart PH.15) — Create K8s manifest for the ML service. Today it exists only
  in `scripts/docker-compose.yaml`. Mirror the controller pattern: `Deployment`,
  `Service` (gRPC 50051 + metrics 8082), `ServiceAccount`, RBAC if needed,
  gRPC liveness probe (after PH.10 lands `grpc_health_v1`). **Effort: M**.

## Group B — Test & CI (steering ≥ 90 % gate) [serial, blocks merge enforcement]

- [ ] **PH.11** — Fix `replay/window_test.go::TestParseWindow_MixedDurationAndTimestamp`.
  Build is currently red. Diagnose via `go test ./internal/replay/ -run
  TestParseWindow_MixedDurationAndTimestamp -v`. **Effort: S**.

- [ ] **PH.12** — Create `.github/workflows/ci.yml`.
  Stages:
  1. `unit-test` — `docker run golang:1.25 go test -coverprofile=cov.out
     -covermode=atomic ./...; go tool cover -func=cov.out | grep total | awk
     '{exit ($3+0 < 90)}'` and `pytest --cov=server --cov-fail-under=90`.
  2. `build-dev` — multi-arch `docker buildx build --platform
     linux/amd64,linux/arm64 -t <harbor>/staffops/...:<sha>`.
  3. `demo` — spin docker-compose, hit `/healthz`, verify a synthetic anomaly.
  - **Effort: M** (pipeline plumbing + test helpers).

- [x] **PH.9** — Bring Go controller coverage from **35 %** to **≥ 90 %**. **DONE (2026-06-30, 90.4%)**.
  Priority order (highest-value paths first per `dev-environment.md`):
  1. `internal/alert/dispatcher.go` — Fire / FireCorrelated branches, dry-run,
     dedup, error wrapping. (currently 0 %)
  2. `internal/correlation/correlator.go` — workload pattern detection,
     dedup window, severity escalation. (currently 2 %)
  3. `internal/enrichment/engine.go` — bundle execution, cache, identity
     extraction. (currently 0 %)
  4. `internal/baseline/store.go` — Welford / EWMA math, key generation,
     evaluate edge cases. (currently 0 %)
  5. `internal/suppression/suppression.go` — namespace match, static-only.
     (currently 0 %)
  6. `internal/redis/client.go` + `internal/ratelimit/limiter.go` +
     `internal/ml/client.go`. (currently 0 % each)
  - Style: table-driven, with mocks for HTTP/Redis. Cover error paths
    explicitly, not just happy paths.
  - **Effort: L** (largest single task in the spec).

- [x] **PH.10** — Bring Python ML coverage from **0 %** to **≥ 90 %**. **DONE (2026-06-30, 98.44%)**.
  - `tests/test_forecaster.py` — Prophet wrapper (mock `Prophet.fit/predict`),
    breach-prediction logic, confidence calculation.
  - `tests/test_multivariate.py` — Isolation Forest wrapper, canonical feature
    padding, contributors selection, history-bound fitting.
  - `tests/test_server.py` — gRPC servicer via in-process fake context +
    injected stubs. Covers Forecast, DetectMultivariate, Health, and `serve()`.
  - Added `--cov=server --cov-fail-under=90` and `pytest-cov` to `pyproject.toml`;
    `server/generated/*` omitted from the gate.
  - Fixed committed `server/generated/ml_pb2_grpc.py` to a package-relative
    import (`from server.generated import ml_pb2`) so the module is importable
    outside the Docker build (the Dockerfile `sed` is now a no-op).
  - CI `test-ml` gate armed (removed the `exit 5` allowance).
  - **Effort: L**.

## Group C — Org-neutrality completion [parallelizable, S each]

- [x] **PH.13** (DONE 2026-07-02) — Move `github.com/karlipegomes/staffops-otel-libs/go` to org
  repo. Steps:
  1. Create `github.com/staffops/staffops-otel-libs` (or equivalent org path).
  2. Tag `v0.x.x` release.
  3. `go.mod` `replace` line during transition; eventual `require` of the new
     path with the tagged version.
  4. Document the move in the new repo's README.
  5. Remove `karlipegomes` references from `go.mod` and `go.sum`.
  - **Effort: S** (mostly process / repo creation).

- [x] **PH.14** (done in chart PH.15) — Move BDC-specific URLs out of the in-repo ConfigMap.
  - File: `controller/deploy/redis.yaml` (the embedded ConfigMap, lines ~110-160).
  - Move `vm-cluster-vmselect.monitoring:8481`, `loki-gateway.monitoring:80`,
    `prometheus-alertmanager.monitoring:9093`, `anomaly-redis.monitoring:6379`,
    `anomaly-worker.monitoring:50052`, `anomaly-ml.monitoring:50051` into
    Helm values (`values-{dev,hml,prd}.yaml`).
  - **Effort: S** (coordinated with PH.15).

## Group D — Helm + ArgoCD [serial, M each]

- [x] **PH.15** (DONE 2026-07-02) — Create `helm-charts/anomaly-detection/`.
  Layout per the gitops review recommendation:
  ```
  helm-charts/anomaly-detection/
  ├── Chart.yaml
  ├── values.yaml
  ├── values-dev.yaml
  ├── values-hml.yaml
  ├── values-prd.yaml
  └── templates/
      ├── _helpers.tpl
      ├── namespace.yaml
      ├── serviceaccount.yaml
      ├── rbac.yaml
      ├── configmap.yaml
      ├── controller-deployment.yaml
      ├── worker-deployment.yaml
      ├── redis-deployment.yaml + redis-pvc.yaml
      ├── redis-service.yaml
      ├── worker-service.yaml
      ├── ml-deployment.yaml
      ├── ml-service.yaml
      ├── pdb.yaml
      ├── networkpolicy.yaml
      ├── vmrule.yaml
      ├── vmservicescrape.yaml
      └── externalsecret.yaml
  ```
  All resources templated with values; absorbs PH.1, PH.6, PH.7, PH.14, PH.18, PH.19, PH.21.
  **Effort: M-L**.

- [x] **PH.16** (DONE 2026-07-02 via helmfile, not ApplicationSet) — Create ArgoCD `ApplicationSet` (matrix generator: cluster ×
  env). Reference Helm values per env. Include `automated.prune`,
  `automated.selfHeal`, `retry.limit: 3`. **Effort: M**.

- [x] **PH.17** (done in chart PH.15) — Add `argocd.argoproj.io/sync-wave` annotations:
  `-2` namespace, `-1` Redis + ServiceAccounts + RBAC, `0` controller + worker
  + ML, `1` PrometheusRule + ServiceMonitor + Dashboard. **Effort: S** (in PH.15
  templates).

- [x] **PH.18** (done in chart PH.15) — Add `PodDisruptionBudget`:
  - controller: `minAvailable: 1` (preserves leader)
  - worker: `minAvailable: 2` (out of 3)
  - **Effort: S** (in PH.15 templates).

- [x] **PH.19** (done in chart PH.15) — Replace `prometheus.io/scrape` annotations with
  `ServiceMonitor` CRDs. Also: define `PrometheusRule` for staffops_ad alerts already
  in `controller/deploy/vmrules.yaml`. **Effort: S**.

- [x] **PH.20** (done in chart PH.15) — Remove explicit CPU limits from controller + worker
  deployments (keep memory limits; ScaleOps manages CPU). Document in chart
  comments why. **Effort: S**.

## Group E — Network & secrets [parallelizable]

- [x] **PH.21** (done in chart PH.15) — Add `NetworkPolicy`:
  - Redis: ingress only from controller+worker pods.
  - Worker gRPC (50052): ingress only from controller pods.
  - ML gRPC (50051): ingress only from controller pods.
  - Egress: controller → Prometheus, Loki, Alertmanager external endpoints.
  - **Effort: S** (in PH.15 templates).

- [ ] **PH.22** — Pre-provision a zero-permission IRSA role.
  - Create IAM role `staffops-anomaly-controller-<env>` with empty policy.
  - Add `eks.amazonaws.com/role-arn` annotation on controller ServiceAccount.
  - Document path to add scoped policies later (S3 model storage, Secrets
    Manager direct read).
  - **Effort: S**.

- [x] **PH.23** (done in chart PH.15) — Worker RBAC: drop `events list/watch`. Only the controller
  uses `EventWatcher`. Validate post-change that worker pods do not error
  on missing permissions. **Effort: S**.

## Group F — Dependency hygiene [parallelizable]

- [ ] **PH.24** — Bump `grpcio` from 1.62.1 to ≥ 1.65.x in `ml/pyproject.toml`
  and `ml/Dockerfile`. Re-test gRPC client paths via the test suite (PH.10).
  CVE-2024-7246. **Effort: S**.

- [ ] **PH.25** — Resolve duplicate dependency pinning in `ml/Dockerfile`.
  Two options:
  - Drop the `RUN pip install <pinned>` block; use `pip install -e '.'` from
    `pyproject.toml` (single source of truth).
  - Or auto-generate the Dockerfile pip block from `pyproject.toml` in CI.
  - **Effort: S**.

## Phase 1 status table — what's actually delivered

| Group | Tasks | Status |
|-------|-------|:------:|
| A. Kyverno admission | PH.1 – PH.8 | 🟡 mostly done (PH.1/6/7/8 in chart; PH.2/PH.5 done; PH.3 golden images pending) |
| B. Test & CI | PH.9 – PH.12 | ✅ done (PH.9/10 coverage gates armed, PH.11 fixed, PH.12 CI live) |
| C. Org-neutrality | PH.13 – PH.14 | 🟡 PH.14 done in chart; PH.13 (otel-libs → org repo) pending |
| D. Helm + ArgoCD | PH.15 – PH.20 | 🟡 chart done (PH.15/17/18/19/20); PH.16 ApplicationSet pending |
| E. Network & secrets | PH.21 – PH.23 | 🟡 PH.21/23 in chart; PH.22 IRSA ARN + PH.4 AWS secret pending |
| F. Dependency hygiene | PH.24 – PH.25 | 🟡 PH.24 done; PH.25 pending |

## Promotion triggers (when this spec is "done")

The spec is complete and can be archived (with the milestone bump per
`version-management.md`) when:

1. All 25 tasks above are checked.
2. A test deploy to a dev cluster passes Kyverno admission with zero policy
   violations.
3. CI pipeline runs green on a clean push, gating on coverage ≥ 90 %.
4. ArgoCD sync of the chart against a dev cluster brings up the full stack
   (controller, worker, ML, Redis) and `/readyz` returns 200 on every pod.

Until then, this spec is **gating Phase 5** — see `ROADMAP.md`.
