# Design: CI/CD Pipeline

## Architecture

```
Push to dev ──→ test.yml (lint + tests + dep scan)
             ──→ build.yml (matrix: controller, worker, ml)
             ──→ sast.yml (gosec + bandit)

PR to main ──→ test.yml (guard: must come from dev)
            ──→ docs.yml (mkdocs build --strict)

Merge to main ──→ docs.yml (deploy to GitHub Pages)

Tag v* ──→ release.yml (build + scan + push + GitHub Release)
```

## Components

| Component | Responsibility |
|-----------|---------------|
| `test.yml` | Quality gate: lint, test, dep scan, branch guard |
| `build.yml` | Build + scan + push dev images (matrix: 3 images) |
| `release.yml` | Tagged release: immutable image + GitHub Release |
| `sast.yml` | Static analysis: gosec (Go) + bandit (Python) |
| `docs.yml` | MkDocs validation on PR, deploy on merge |

## Rationale

### Decision 1: Docker Hub over ghcr.io

**Choice**: Publish images to Docker Hub private repository.

**Justification**:
1. Private repos on ghcr require GitHub Advanced Security (paid) for vulnerability scanning integration
2. Docker Hub private repos already provisioned for the org
3. Existing tooling (Trivy, DependencyTrack) integrates with Docker Hub

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Extra secret management (DOCKERHUB_USERNAME/TOKEN) | Acceptable — already managed via GitHub Secrets |
| No native GitHub UI integration for images | Acceptable — images consumed by Helm/ArgoCD, not GitHub UI |

**When this decision would be wrong**:
- GitHub Advanced Security becomes available and ghcr offers better integration with Actions
- Docker Hub pricing changes make private repos expensive

### Decision 2: Report-only security gates

**Choice**: Security/lint gates run in report-only mode (`continue-on-error: true`, Trivy `exit-code: 0`).

**Justification**:
1. Existing CVE debt in dependencies would permanently block all builds if gates were blocking
2. Existing lint debt (gofmt, ruff) would block all PRs
3. Visibility into findings is immediate; enforcement is phased

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Known vulnerabilities can ship | Acceptable during rollout — debt tracked, no new prod deploy without manual review |
| Developers may ignore warnings | Mitigated by tracking debt list and re-arming gates as items clear |

**When this decision would be wrong**:
- Critical CVE found that should block release immediately — override manually
- Debt clears and gates are not re-armed (process failure)

### Decision 3: Guard job for branch model enforcement

**Choice**: Custom `guard` job in test.yml checks PR source branch = `dev`.

**Justification**:
1. GitHub branch protection rules cannot restrict *source* branch of PRs (only target)
2. Guard job is simple shell check, zero external dependency
3. Provides clear error message when violated

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| Not a hard block (can be bypassed by admin merge) | Acceptable — team discipline + visible CI failure |
| Adds a job to every PR | Negligible runtime (~5s) |

**When this decision would be wrong**:
- GitHub adds native source-branch restriction in branch protection rules

### Decision 4: Matrix strategy for build

**Choice**: Single `build.yml` with matrix strategy building 3 images in parallel.

**Justification**:
1. Three independent images (controller, worker, ml) from same repo — natural parallelism
2. Shared steps (Trivy scan, multi-arch push, SBOM) are identical per image — DRY via matrix
3. Single workflow file easier to maintain than 3 separate ones

**Trade-offs accepted**:
| Cost | Reality |
|------|---------|
| One image failure blocks the whole matrix (default) | Acceptable — `fail-fast: false` if needed later |
| Matrix config slightly more complex than 3 simple workflows | Acceptable — maintenance benefit outweighs |

**When this decision would be wrong**:
- Images diverge significantly in build steps (different base images, different registries)

### Decision 5: Template origin

**Choice**: Workflows inherited from `staffops-aigent-squad` template, adapted for multi-language and multi-image.

**Justification**:
1. Proven template already handles Trivy, multi-arch, Docker Hub push
2. Adapted for Go+Python (dual lint, dual test) and 3-image matrix
3. Consistency across staffops repos

## Invariants

- Images are NEVER pushed without Trivy scan completing (even in report-only mode)
- Release images use immutable version tag from git tag (never `latest` in release)
- Guard job ALWAYS runs on PRs to main (cannot be skipped)
- SBOM is generated for every image build

## Dependencies

| Service | Purpose |
|---------|---------|
| Docker Hub | Container registry (private) |
| GitHub Actions | CI/CD runner |
| GitHub Pages | Documentation hosting |
| Trivy | Container + filesystem vulnerability scanning |
| gosec | Go static security analysis |
| bandit | Python static security analysis |
| ruff | Python linting |
| mkdocs-material | Documentation site generator |
