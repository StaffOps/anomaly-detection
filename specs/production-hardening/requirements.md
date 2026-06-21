# Production Hardening — Requirements

## Context

Established 2026-06-16 after a multi-specialist evaluation (`dev`, `security`,
`gitops`, `anomaly-detection`) of `staffops-anomaly-detection` `controller 0.7.0`
+ `ml 0.2.0`. The reviewers corroborated the project's intellectual maturity (the
threat-model and Decision 8 hold up to independent review) but flagged 25 distinct
production gaps across four categories — none of them architectural, all of them
mechanical enforcement of existing steering rules.

This spec captures those gaps so they can be tracked, sized, and executed in
parallel with Phase 0 (strategic gates). It exists as a **dedicated spec**, not a
single bullet inside `P5.2 — Deploy to cluster`, because the previous compression
hid both the blast radius and the effort.

There is **no `design.md`** for this spec. There are no design choices to make —
each item is "apply the steering rule that says X." Adding a design doc would
be ceremony.

## User Stories

### As an SRE preparing the cluster deploy

- WHEN the manifests are submitted to a BDC cluster THEN Kyverno admission SHALL
  accept them without errors (`securityContext`, mandatory labels, no `:latest`,
  golden bases, `preStop` hook, image signature verification).
- WHEN the controller pod starts in production THEN it SHALL run as non-root,
  with read-only rootfs, with no shell capabilities, and with all secrets
  file-mounted (not env-var defined).
- WHEN any pod terminates THEN it SHALL drain in-flight work via `preStop` and
  `terminationGracePeriodSeconds: 30` before SIGTERM is sent.

### As a security reviewer

- WHEN I scan the deployed images THEN every base image SHALL be apko-built
  golden + cosign-signed, traceable to a tagged release.
- WHEN I audit the namespace THEN Redis SHALL require AUTH and the password
  SHALL be sourced from External Secrets Operator backed by AWS Secrets Manager
  (file-mounted, never inline env-var).
- WHEN I list ingress paths to Redis, worker gRPC, and ML gRPC THEN
  `NetworkPolicy` SHALL restrict them to controller (and worker for Redis), not
  arbitrary pods in the namespace.
- WHEN I inspect `go.mod` THEN there SHALL be no personal-account dependencies;
  the `staffops-otel-libs` Go module SHALL be in the org repo with a tagged
  release.

### As a developer reading the test suite

- WHEN I run the Go test suite THEN coverage SHALL be ≥ 90 % across `internal/`
  packages, with the gate enforced in CI (`go test -coverprofile + threshold`).
- WHEN I run the Python ML test suite THEN coverage SHALL be ≥ 90 %, enforced
  via `pytest --cov-fail-under=90` in CI.
- WHEN I push to the repo THEN `unit-test → build-dev → demo` stages SHALL run
  via `.gitlab-ci.yml`, blocking merge on any failure.
- WHEN I check the build status THEN no test SHALL be failing
  (currently `replay/window_test.go::TestParseWindow_MixedDurationAndTimestamp`
  is red).

### As a GitOps operator

- WHEN I deploy to a new environment (dev / hml / prd) THEN I SHALL apply a
  Helm chart with environment-specific values, not raw YAML with hand-edited
  `REPLACE_ME` placeholders.
- WHEN I add a new cluster THEN an ArgoCD `ApplicationSet` SHALL pick it up via
  cluster selector, with no per-cluster YAML duplication.
- WHEN ArgoCD syncs THEN Redis SHALL come up before controller/worker (via
  `sync-wave` annotations).
- WHEN a pod is voluntarily evicted THEN a `PodDisruptionBudget` SHALL preserve
  the controller leader and 2-of-3 workers.

## Acceptance Criteria

The spec is complete when all the following are true and verifiable:

### Kyverno admission
- [ ] All four pod types (controller, worker, redis, ML) define `securityContext`
      with `runAsNonRoot`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation:
      false`, `capabilities.drop: [ALL]`, `runAsUser: 65534`.
- [ ] No manifest references `:latest`. All image tags are CI-driven (SHA-based
      or version).
- [ ] All base images are BDC golden (apko, cosign-signed) routed through
      Harbor proxy.
- [ ] All pod templates carry `CostCenter`, `Environment`, `app.kubernetes.io/name`,
      `app.kubernetes.io/version` labels.
- [ ] All pod templates have `lifecycle.preStop` (`sleep 5`) and
      `terminationGracePeriodSeconds: 30`.
- [ ] ML service has its own K8s manifest (not just `docker-compose`).

### Test & CI
- [ ] Go coverage ≥ 90 % across `internal/...` (currently 35 %).
- [ ] Python ML coverage ≥ 90 % (currently 0 %).
- [ ] `replay/window_test.go::TestParseWindow_MixedDurationAndTimestamp` passes.
- [ ] `.gitlab-ci.yml` exists with `unit-test → build-dev → demo` stages, each
      failing the build on coverage / lint regression.

### Org-neutrality completion
- [ ] `go.mod` no longer references `github.com/karlipegomes/...`. Module path
      is org-rooted with a tagged release.
- [ ] BDC-specific URLs are no longer in the in-repo ConfigMap; they live in
      Helm values.

### GitOps
- [ ] `helm-charts/anomaly-detection/` exists with templates for every
      resource currently in `controller/deploy/`.
- [ ] ArgoCD `ApplicationSet` with matrix generator (cluster × env) targets
      the chart.
- [ ] `sync-wave` annotations sequence Redis before controller/worker.
- [ ] `PodDisruptionBudget` exists for controller and worker.
- [ ] `prometheus.io/scrape` annotations replaced by `VMServiceScrape` CRDs.
- [ ] CPU limits removed from controller and worker.
- [ ] Redis has a `PersistentVolumeClaim`.

### Network & secrets
- [ ] `NetworkPolicy` restricts Redis ingress to controller+worker, worker
      gRPC to controller, ML gRPC to controller.
- [ ] Controller `ServiceAccount` carries `eks.amazonaws.com/role-arn`
      (zero-permission IRSA role, scoped policies added later).
- [ ] Worker RBAC no longer grants `events list/watch` (only the controller
      uses `EventWatcher`).
- [ ] Redis runs with AUTH; password sourced from `ExternalSecret`
      file-mounted, never inline env-var.

### Dependency hygiene
- [ ] `grpcio` ≥ 1.65.x in `pyproject.toml` and `ml/Dockerfile` (consistent).
- [ ] `ml/Dockerfile` no longer hardcodes pinned versions that overlap with
      `pyproject.toml`. Single source of truth.

## Out of scope

- Any algorithmic change to detection (those live in Phase 0 / Phase 2).
- The strategic decision about whether the project becomes a product or
  collapses into "internal tooling + Robusta playbook" — that is the output
  of Phase 0 gates P0.1 / P0.2, not this spec.
- Falco integration, agent-API integration, ML Forecast wiring — separate specs.
- Self-monitoring VMRules (P6.1) — separate roadmap item.

## Cross-references

- Roadmap: see `ROADMAP.md` → Phase 5 Pre-Reqs (PH.1 – PH.25).
- Threat model: `docs/threat-model-and-limitations.md` → Additional concerns
  from independent security review (2026-06-16).
- Steering: `dev-environment.md` (≥ 90 % coverage gate),
  `k8s-best-practices.md` (mandatory labels, securityContext, preStop),
  `cloud-security.md` (golden images, IRSA, secrets), `12-factor-app.md`
  (file-mounted secrets), `ci-cd-conventions.md` (multi-arch, image signing).
