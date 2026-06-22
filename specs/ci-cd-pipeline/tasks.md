# Tasks: CI/CD Pipeline

> **Status**: `DONE` — Implemented 2026-06-22. Report-only gates to be re-armed as debt clears.

- [x] Task 1: Create test.yml with guard, lint-go, lint-ml, dep_scan, test-go, test-ml jobs
- [x] Task 2: Create build.yml with matrix strategy (controller, worker, ml), Trivy scan, multi-arch push to Docker Hub, SBOM generation
- [x] Task 3: Create release.yml triggered by v* tag / workflow_dispatch, immutable version tag, GitHub Release creation
- [x] Task 4: Create sast.yml with gosec (Go) + bandit (Python)
- [x] Task 5: Create docs.yml with mkdocs build --strict on PRs, deploy to GitHub Pages on merge to main
- [x] Task 6: Configure DOCS_DEPLOY_TOKEN for private staffops-otel-libs module access
- [x] Task 7: Configure Docker Hub secrets (DOCKERHUB_USERNAME, DOCKERHUB_TOKEN)
- [x] Task 8: Set report-only mode on security gates (continue-on-error, exit-code: 0)
- [x] Task 9: Validate all workflows green on current main
- [x] Task 10: Document debt list for re-arming gates (tracked in repo issues)
