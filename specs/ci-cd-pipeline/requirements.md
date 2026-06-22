# Feature: CI/CD Pipeline (GitHub Actions)

## User Stories

### As a developer pushing code

WHEN I push to `dev` or open a PR THEN the system SHALL run lint, tests, dependency scanning, and SAST automatically so I get fast feedback on code quality.

WHEN I push to `dev` THEN the system SHALL build container images, scan them with Trivy, and push to Docker Hub so I can test in dev environments.

### As a maintainer releasing

WHEN I push a `v*` tag or trigger the release workflow manually THEN the system SHALL build, scan, and push images with the immutable version tag and create a GitHub Release.

WHEN I merge a PR to `main` THEN the system SHALL enforce that the PR originated from the `dev` branch only.

### As a security reviewer

WHEN any PR is opened THEN the system SHALL run SAST (gosec + bandit) and dependency scanning (Trivy filesystem) so I can review findings before merge.

WHEN images are built THEN the system SHALL generate CycloneDX SBOMs and attach them as artifacts.

## Workflows

### test.yml

| Job | Tool | Scope |
|-----|------|-------|
| `guard` | Branch check | Reject PRs to `main` not from `dev` |
| `lint-go` | `gofmt` + `go vet` | Go source |
| `lint-ml` | `ruff` | Python ML source |
| `dep_scan` | Trivy filesystem | All dependencies |
| `test-go` | `go test -cover` | Go unit tests with coverage |
| `test-ml` | `pytest` | Python ML unit tests |

### build.yml

- Matrix strategy: 3 images (`controller`, `worker`, `ml`)
- Per image: build local → Trivy scan → push multi-arch to Docker Hub
- SBOM generation (CycloneDX) attached as build artifact
- Triggered on push to `dev`

### release.yml

- Triggered by `v*` tag push or `workflow_dispatch`
- Same scan+push flow as build.yml but with immutable version tag
- Creates GitHub Release with changelog and SBOM artifacts

### sast.yml

- `gosec` for Go source
- `bandit` for Python ML source
- Triggered on PRs

### docs.yml

- On PRs: `mkdocs build --strict` (validation only)
- On merge to `main`: deploy to `staffops.github.io/anomaly-detection/`

## Branch Model

- `main` + `dev` branches
- Guard job enforces: PRs to `main` must originate from `dev` only
- All feature work merges to `dev` first

## Report-Only Gates

During rollout, security/lint gates run in report-only mode (`continue-on-error: true` / `exit-code: 0`):
- Trivy dep scan
- gosec / bandit
- ruff / gofmt warnings

Debt to re-arm as existing CVE/lint issues are resolved.

## Private Module Auth

- `DOCS_DEPLOY_TOKEN` secret used for `staffops-otel-libs` private Go module access
- Configured via `git config` in workflow steps

## Registry

- Images published to **Docker Hub** (private repository)
- Not ghcr.io

## Acceptance Criteria

- [x] All 5 workflows (test, build, release, sast, docs) are operational
- [x] Workflows are green on current `main`
- [x] Guard job blocks PRs to main not from dev
- [x] Build produces multi-arch images for all 3 components
- [x] Trivy scan runs on every build (report-only during rollout)
- [x] SAST runs on every PR (report-only during rollout)
- [x] Release workflow creates GitHub Release with version tag
- [x] MkDocs deploys to GitHub Pages on merge to main
- [x] Private module auth works for staffops-otel-libs

## Out of Scope

- Blocking enforcement of security gates (deferred until debt clears)
- Image signing with cosign (future phase)
- Deployment to Kubernetes clusters (separate GitOps concern)
- Branch protection rules on GitHub (manual setup, not in workflow)
