# Handoff

Last updated: 2026-11-23 by Backstage docs-standard pilot · branch `main`

## State

Last release is `controller` **v0.11.0** (tag `v0.11.0`, 2026-07-19); `ml` is at 0.3.0 per the
[CHANGELOG](https://github.com/StaffOps/anomaly-detection/blob/main/CHANGELOG.md). Per
[AGENTS.md](https://github.com/StaffOps/anomaly-detection/blob/main/AGENTS.md) it is deployed to
homolog (cluster `devops-core`, namespace `staffops`) in **dry-run** mode via the Helm chart, and the
repo is trunk-based on `main` (branch `dev` retired 2026-07-13). The `[Unreleased]` section holds
detection fixes from 2026-07-19..22 that are not yet tagged. Since then only catalog and funding
files changed. CI on `main`: **Build & Push** and **SAST** pass; **Test** fails in `lint-ml` (ruff
`I001` unsorted imports) and `dep_scan` (Trivy: 9 HIGH, e.g. `github.com/klauspost/compress`
CVE-2026-63209, fixed in 1.18.7); `test-go`, `test-ml` and `lint-go` pass.

## Done

- Backstage `catalog-info.yaml` with technology tags and TechDocs (`04edb80`, `44ae0d5`)
- `min_value` absolute floor on adaptive rules; namespace labels resolved across the rule set (`1d57c0b`)
- Replay: anti-poison gate coverage, in-memory baseline parity with production (`c58be88`, `b799b1f`)
- P0.1 injection measurement and FDR-vs-noise-profile finding recorded (`50c1ade`)
- v0.11.0: FDR over the full test family, direction-of-badness, tuned rule set (`5fc4784`)
- Controller/worker and ML metrics routed through staffops-otel-libs (`e53e60f`, `462c805`)
- Per-workload adaptive suppression and its metric (`1aa5cd4`)

## In progress

- No open pull requests or issues.
- [Roadmap](site/roadmap.md) Phase 0 gates: P0.1 measurement (harness built; see the
  [ROADMAP](https://github.com/StaffOps/anomaly-detection/blob/main/ROADMAP.md) for status),
  P0.2 competitive teardown (spec ready, not executed), P0.3 degradation-model validation.

## Next steps

1. Fix the red **Test** workflow: `ruff check --fix` on `ml/` and bump `klauspost/compress` and the other Trivy HIGH findings (suggested).
2. Cut a release for the `[Unreleased]` detection fixes once validated in homolog (suggested).
3. Per AGENTS.md, remaining work is: exit dry-run, CI-published images as the deploy record, and the P0.1 recall/FP number.

## Decisions

Recorded as numbered decisions in [Architecture decisions](site/architecture/decisions.md), including:

- Decision 4: dry-run as the default mode
- Decision 8: the detector is commodity; the product hypothesis is causal incident origination ([hypothesis](site/hypothesis-causal-origination.md))
- Decision 9: local homolog build deployed; CI publishes the versioned image as the record
- Decision 10: `controller/config.yaml` (local/replay) and the deployed Helm values `detection:` are allowed to diverge

## Risks and open questions

- Deployed rules live in a per-cluster Helm values override outside this repo; editing `controller/config.yaml` does not change the cluster (Decision 10).
- The `dev` branch still exists although retired; it may confuse tooling that assumes dev -> main (inferred).
- Known limitations are listed in [Threat model and limitations](site/threat-model-and-limitations.md).

## How to verify

```bash
gh run list -R StaffOps/anomaly-detection -L 5
docker run --rm -v "$(pwd)/controller":/src -w /src -e GOPRIVATE='github.com/staffops/*' -e GONOSUMDB='github.com/staffops/*' golang:1.26 go test ./...
docker run --rm -v "$(pwd)":/src -w /src python:3.11-slim sh -c \
  "pip install -q ruff && ruff check ml/ --exclude 'ml/server/generated/*'"
```
