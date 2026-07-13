# Competitive Teardown — Requirements

## Context

Time-boxed experiment (P0.2) that decides whether to build a product or ship
config. After four rounds of design review, the detector was proven commodity
(Decision 8). The surviving hypothesis is that **causal incident origination**
(intra-runtime chain ordering) is not expressible as declarative config.

This experiment tests that hypothesis by attempting to reproduce each
capability in incumbent tools (Prometheus-compatible TSDB rules, Alertmanager config,
Robusta playbooks).

## User Stories

### As the project owner

- WHEN the experiment completes THEN I SHALL have a binary decision
  (product / not-product / product-minimum) backed by evidence, not opinion.
- WHEN a capability ports to config THEN the experiment SHALL produce the
  actual config artifact (rule, routing, playbook) ready to ship.
- WHEN a capability resists THEN the experiment SHALL name the exact fraction
  that resists and why.

### As an SRE evaluating the system

- WHEN I read the experiment results THEN I SHALL see each capability
  classified as PORTS / RESISTS / PARTIAL with effort-hours and evidence.
- WHEN a capability ports THEN I SHALL receive the shippable config (vmrule,
  Alertmanager routing, Robusta playbook) that replaces the custom code.

## Acceptance Criteria

- [ ] Capabilities C1-C5 tested against incumbents with verdict per capability
- [ ] Capability C6 (causal chain ordering) explicitly attacked via Robusta
      playbook AND recording rules
- [ ] Each verdict includes: effort (hours), artifact produced, what was lost
- [ ] Decision criteria applied mechanically (PORTS/RESISTS defined a priori)
- [ ] `docs/hypothesis-causal-origination.md` gate 2 updated with verdict
- [ ] ROADMAP P0.2 marked with result

## Out of scope

- Building the causal product (that's post-experiment, if C6 resists)
- Detector benchmarking (that's P0.1 synthetic-injection)
- Deploying Robusta to production (experiment only, local/test)

## Cross-references

- Full experiment protocol: [`experiment.md`](experiment.md)
- Hypothesis: `docs/hypothesis-causal-origination.md`
- Degradation model (source of causal chains): `docs/architecture/degradation-model.md`
- Decision 8 (detector = commodity): `docs/architecture/decisions.md`
