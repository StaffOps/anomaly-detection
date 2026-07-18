# Tasks: Self-Monitoring PrometheusRules (P6.1)

> **Status**: `TODO` — Small, blocked on Helm chart (PH.15)

- [ ] Task 1: Write PrometheusRule YAML with all 7 alert rules, expressions, for durations, and labels
- [ ] Task 2: Add `templates/prometheusrule.yaml` to Helm chart with `.Values.prometheusRule.enabled` toggle
- [ ] Task 3: Validate rules with `promtool check rules` (Docker-based, add to CI)
- [ ] Task 4: Add `runbook_url` annotations pointing to runbook paths (placeholder URLs until runbooks written)
- [ ] Task 5: Add Helm chart tests (`helm template` + assert PrometheusRule rendered with correct rules)
