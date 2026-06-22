# Feature: Degradation Model Validation (P0.3)

## Context

P0.3 gate. The degradation model in `docs/architecture/degradation-model.md` describes causal chains (.NET, Go, Python runtime degradation patterns). Until validated against real incidents, it's a written hypothesis.

**Goal**: Confirm causal chains against real incidents via replay. Walk each chain backwards from symptom, check leading→lagging ordering.

## User Stories

WHEN I validate a causal chain against replay data THEN I have evidence the model matches reality (or evidence it doesn't) — enabling confidence in the model or targeted corrections.

WHEN causal chains are validated THEN SREs can trust the degradation model for runbooks and alerting logic.

## Acceptance Criteria

- [ ] At least 3 causal chains walked against replay data from real incidents
- [ ] Each chain scored: confirmed (leading precedes lagging) / refuted / insufficient data
- [ ] `degradation-model.md` updated with validation status per chain
- [ ] Results feed into P0.2 competitive teardown (C6 depends on model validity)

## Out of Scope

- Implementing new detectors
- Changing the model (just validating it)

## Dependencies

- Replay mode (done)
- Historical incident data available via VictoriaMetrics/Loki
