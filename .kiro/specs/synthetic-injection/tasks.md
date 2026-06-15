# Tasks: Synthetic Fault Injection on Replay

> Verification independence (steering): autor do código ≠ autor dos testes. Onde
> houver subagents, delegar implementação a `dev` e testes a sessão `dev` distinta,
> review por `code-review`. Gate de cobertura ≥90% no novo código (`internal/replay/inject/`).
> Builds via Docker (`golang:1.22-alpine`), sem SDK local.

## Phase 1: Injection core (`internal/replay/inject/`)

- [ ] Task 1: `InjectionConfig` + parse YAML do perfil (targets, type, window, magnitude, seed)
- [ ] Task 2: `GroundTruth` struct + registro acumulado por run (depends on: Task 1)
- [ ] Task 3: `faultFunc` para `spike` — pico transiente em janela curta (depends on: Task 1)
- [ ] Task 4: `faultFunc` para `ramp` — crescimento linear 0→magnitude na janela (o caso crítico) (depends on: Task 1)
- [ ] Task 5: `faultFunc` para `step` — salto de nível sustentado (depends on: Task 1)
- [ ] Task 6: `faultFunc` para `silence` — remoção de pontos na janela (depends on: Task 1)
- [ ] Task 7: `Injector.Apply([]TimeSeries) []TimeSeries` — aplica fault funcs às séries-alvo, emite GroundTruth, no-op sem perfil, determinístico por seed (depends on: Task 2-6)

## Phase 2: Scoring (`internal/replay/inject/scorer.go`)

- [ ] Task 8: Fingerprint normalizado de target (metric + labels) pro matching (depends on: Task 2)
- [ ] Task 9: `Scorer.Score(report, []GroundTruth)` — classifica TP/FP/FN com grace window (depends on: Task 8)
- [ ] Task 10: Computar precision/recall/F1 + recall_by_type + detection_latency (depends on: Task 9)

## Phase 3: Integração no replay

- [ ] Task 11: Hook em `replay.Run` — chamar `Injector.Apply` após `QueryRange`, antes de acumular metricSeries; no-op quando sem `--inject` (depends on: Task 7)
- [ ] Task 12: Flag CLI `--inject=<perfil>|none` em `cmd/controller/main.go` (depends on: Task 11)
- [ ] Task 13: Chamar `Scorer` após o loop; anexar blocos `injection` + `scoring` ao Report (depends on: Task 10, Task 11)
- [ ] Task 14: Estender serializador JSON do replay com os dois novos blocos (depends on: Task 13)
- [ ] Task 15: Confirmar invariantes herdadas — sem Redis/AM/gRPC/ML; injeção só em memória (depends on: Task 11)

## Phase 4: Testes (autor distinto — verification-independence)

- [ ] Task 16: Testes de cada `faultFunc` — forma correta da perturbação, determinismo por seed (depends on: Task 3-6)
- [ ] Task 17: Testes do `Injector` — no-op sem perfil, múltiplas injeções, ground truth correto (depends on: Task 7)
- [ ] Task 18: Testes do `Scorer` — TP/FP/FN, grace window, recall_by_type, latency; casos de label mismatch (depends on: Task 9, Task 10)
- [ ] Task 19: Teste de integração — replay+injeção end-to-end sobre série sintética conhecida: ramp injetada é detectada (ou não — registra o recall) (depends on: Task 13)
- [ ] Task 20: Teste do caso `silence` — confirma recall ~0 esperado em V1 (baseline pra P2.10) (depends on: Task 13)
- [ ] Task 21: Cobertura ≥90% no package `internal/replay/inject/` (depends on: Task 16-20)

## Phase 5: Execução do gate (o objetivo real)

- [ ] Task 22: Escolher 3-5 janelas limpas reais (baixa atividade de alerta histórica) — declarar como ground-truth-limpas
- [ ] Task 23: Rodar `--inject=none` sobre elas → **FP upper-bound** (ruído de base do detector)
- [ ] Task 24: Rodar perfis spike/ramp/step/silence em magnitudes 2σ/3σ/5σ → **recall lower-bound por tipo e magnitude**
- [ ] Task 25: Consolidar os números num relatório de medição; comparar `recall(ramp)` vs `recall(spike)` (testa a cegueira-a-rampa) (depends on: Task 23, Task 24)

## Phase 6: Decisão e documentação

- [ ] Task 26: Atualizar `docs/hypothesis-causal-origination.md` — marcar gate P0.1 com o resultado (✅/refutado) + evidência (depends on: Task 25)
- [ ] Task 27: Atualizar `docs/architecture/decisions.md` Decision 8 — o número confirma ou refuta "detector é commodity" (depends on: Task 25)
- [ ] Task 28: Marcar P0.1 no ROADMAP com o resultado mensurável (depends on: Task 25)
- [ ] Task 29: Doc curto em `docs/operations/` — como rodar uma medição e como ler o número (o que prova / o que não prova) (depends on: Task 22)

## Nota sobre ordenação

Phases 1-4 constroem e validam o harness. **Phase 5 é o objetivo** — o número que
destrava o roadmap. Phase 6 fecha o loop: o número volta pros docs de decisão e
hipótese, transformando "não sei" em evidência. Não declarar P0.1 done antes da
Phase 5 produzir números reais (não só "o harness compila e testa").
