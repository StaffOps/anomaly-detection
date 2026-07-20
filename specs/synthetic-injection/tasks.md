# Tasks: Synthetic Fault Injection on Replay

> **Status**: `READY` — Spec complete, not yet executed (Phase 0.1 gate)

> Verification independence (steering): autor do código ≠ autor dos testes. Onde
> houver subagents, delegar implementação a `dev` e testes a sessão `dev` distinta,
> review por `code-review`. Gate de cobertura ≥90% no novo código (`internal/replay/inject/`).
> Builds via Docker (`golang:1.25-alpine`), sem SDK local.

## Phase 1: Injection core (`internal/replay/inject/`)

- [x] Task 1: `InjectionConfig` + parse YAML do perfil (targets, type, window, magnitude, seed)
- [x] Task 2: `GroundTruth` struct + registro acumulado por run (depends on: Task 1)
- [x] Task 3: `faultFunc` para `spike` — pico transiente em janela curta (depends on: Task 1)
- [x] Task 4: `faultFunc` para `ramp` — crescimento linear 0→magnitude na janela (o caso crítico) (depends on: Task 1)
- [x] Task 5: `faultFunc` para `step` — salto de nível sustentado (depends on: Task 1)
- [x] Task 6: `faultFunc` para `silence` — remoção de pontos na janela (depends on: Task 1)
- [x] Task 7: `Injector.Apply([]TimeSeries) []TimeSeries` — aplica fault funcs às séries-alvo, emite GroundTruth, no-op sem perfil, determinístico por seed (depends on: Task 2-6)

## Phase 2: Scoring (`internal/replay/inject/scorer.go`)

- [x] Task 8: Fingerprint normalizado de target (metric + labels) pro matching (depends on: Task 2)
- [x] Task 9: `Scorer.Score(report, []GroundTruth)` — classifica TP/FP/FN com grace window (depends on: Task 8)
- [x] Task 10: Computar precision/recall/F1 + recall_by_type + detection_latency (depends on: Task 9)

## Phase 3: Integração no replay

- [x] Task 11: Hook em `replay.Run` — chamar `Injector.Apply` após `QueryRange`, antes de acumular metricSeries; no-op quando sem `--inject` (depends on: Task 7)
- [x] Task 12: Flag CLI `--inject=<perfil>|none` em `cmd/controller/main.go` (depends on: Task 11)
- [x] Task 13: Chamar `Scorer` após o loop; anexar blocos `injection` + `scoring` ao Report (depends on: Task 10, Task 11)
- [x] Task 14: Estender serializador JSON do replay com os dois novos blocos (depends on: Task 13)
- [x] Task 15: Confirmar invariantes herdadas — sem Redis/AM/gRPC/ML; injeção só em memória (depends on: Task 11)

## Phase 4: Testes (autor distinto — verification-independence)

- [x] Task 16: Testes de cada `faultFunc` — forma correta da perturbação, determinismo por seed (depends on: Task 3-6)
- [x] Task 17: Testes do `Injector` — no-op sem perfil, múltiplas injeções, ground truth correto (depends on: Task 7)
- [x] Task 18: Testes do `Scorer` — TP/FP/FN, grace window, recall_by_type, latency; casos de label mismatch (depends on: Task 9, Task 10)
- [x] Task 19: Teste de integração — replay+injeção end-to-end sobre série sintética conhecida: ramp injetada é detectada (ou não — registra o recall) (depends on: Task 13)
- [x] Task 20: Teste do caso `silence` — confirma recall ~0 esperado em V1 (baseline pra P2.10) (depends on: Task 13)
- [x] Task 21: Cobertura ≥90% no package `internal/replay/inject/` (depends on: Task 16-20)

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

## Retomada — achados práticos (2026-07-18) + testes mínimos

Rodadas manuais do harness in-cluster (Job on-demand, imagem 0.11.0) contra dados
reais. **Conclusão: o encanamento roda** (harness executa, gera JSON, FDR filtra —
155/420 rejeições observadas), **mas o número de recall não sai** por dois defeitos
concretos na cadeia injeção → detecção → scorer. Ordem de retomada:

### Blocker 1 — a falta injetada não dispara de forma confiável (CENTRAL)
- 3 tentativas (namespace amplo → 1372 séries planas; alvo único de alta variância
  `DataPlatform.People`) deram **recall=0**: a série injetada não virou anomalia,
  apesar do ground truth ser registrado (a falta foi aplicada).
- Causa provável: `faultSpike`/`faultStep` escalam por `seriesStddev` (série plana →
  perturbação ~0), e/ou o EWMA do baseline in-mem absorve a falta sustentada (só o 1º
  tick fica anômalo), e/ou magnitude vs piso de stddev.
- **Teste mínimo (unit, sem cluster)**: injetar falta numa `TimeSeries` sintética de
  stddev conhecido → 1 tick de detecção no in-mem baseline → **assertar z esperado e
  IsAnomaly=true**. Isola injeção→z-score.

### Blocker 2 — casamento detectado ↔ ground truth no scorer
- Na rodada de alvo único: 3 anomalias detectadas contadas como **FP**, a injetada como
  **FN**. O fingerprint do detectado pode não bater com o do ground truth.
- **Teste mínimo (unit)**: ground truth na série X + anomalia detectada na série X →
  **assertar TP** (não FP/FN). Verifica `Fingerprint(metricName, labels)` nos dois lados.

### Blocker 3 — scoring não renderiza no markdown
- `report_md.go` não escreve o bloco Injection/Scoring (só o JSON tem).
- **Teste mínimo**: golden test com injeção ativa → bloco de scoring presente no MD.

### Medição (Phase 5, depois dos 3 blockers)
- Janela limpa + faltas conhecidas em séries **com variância**. Rodar 2x: `fdr_target=1.0`
  (off) vs `0.05` (on). Esperado: recall ~igual, FP menor com FDR on. Esse é o número.
- Infra já provada: Job on-demand + extração do JSON (ver histórico de 2026-07-18).

**Não declarar P0.1 done** até os 3 blockers terem teste unit verde E a Phase 5 produzir
recall/FP reais.

### Status 2026-07-19 — blockers resolvidos (harness agora fiel)

- **Blocker 1 ✅ RESOLVIDO** — causa raiz era um bug: `internal/replay/inmem_baseline.go`
  calculava o z-score **depois** de dobrar a amostra no EWMA/stddev (numerador
  amortecido, denominador inflado pelo próprio spike) e **sem piso de stddev** — ao
  contrário da produção (`internal/baseline/store.go`), que detecta contra o baseline
  **anterior** com piso. O replay sub-detectava vs produção, por isso faltas injetadas
  não disparavam. Reescrito pra espelhar produção (z pré-update + piso + gate anti-poison).
  Teste: `TestInMemStore_SpikeFiresAfterWarmup`. (Off-by-one de warm-up também alinhado:
  `stats.Count < WarmUpSamples`, como produção.)
- **Blocker 2 ✅ NÃO ERA BUG** — o scorer está correto (`TestScore_AllTP`) e
  `toAnomalyEntry` preserva Metric+Labels crus, então o fingerprint do detectado bate
  com o do ground truth. O "FP em vez de TP" era 100% sintoma do Blocker 1 (nada
  injetado disparava).
- **Blocker 3 ✅ RESOLVIDO** — `report_md.go` agora renderiza o bloco "Injection Scoring"
  (precision/recall/F1/TP/FP/FN + recall por tipo) no markdown, não só no JSON.

**Implicação**: todos os números de replay anteriores (incl. os 286/155/420 de
2026-07-18) vieram de um replay que **sub-detectava** — não são confiáveis. Refazer a
medição com o harness corrigido é a Phase 5. Precisa de: imagem nova (com o fix) +
run in-cluster com `--inject`, FDR off vs on.

### Phase 5 — primeira medição real (2026-07-19/20)

Rodada in-cluster com o harness corrigido (imagem `...:0.11.0-p0meas`): 2 Jobs on-demand,
`--replay --inject`, spike (magnitude 12) em 6 serviços de alta variância
(`request_rate_by_service`) + `cpu_by_pod` como ruído ambiente, `--max-anomalies=200000`
(sem cap), FDR **off** (`fdr_target=1.0`) vs **on** (`0.05`).

| | FDR off | FDR on |
|---|---|---|
| Recall (faltas injetadas) | **1.000** (22/22) | **1.000** (21/21) |
| Anomalias totais | 3531 | 3435 |
| FP | 3509 | 3414 |
| FDR rejeitou | 0 | 96 (**~2,7% de FP**) |

**Aprendizados (evidência, não veredito — o veredito do gate é decisão à parte):**

1. **O FDR preserva recall.** Todas as faltas injetadas sobreviveram ao filtro
   (recall 1.000 com e sem FDR). O F0 não derruba sinal real. **Resultado sólido.**
2. **A redução de FP do FDR depende do perfil de ruído / rule set.** Aqui foi só
   **2,7%** — porque `cpu_by_pod` tem variância *genuína* (z alto), o BH corretamente
   o mantém e só corta o tail marginal (96 séries). Em produção, com o set de 18 regras
   tunadas, o mesmo FDR rejeita **18–30%**. Ou seja, **o benefício do FDR não é uma
   constante — escala com quanto ruído *marginal* (z pouco acima de 3) o rule set gera.**
3. **Corolário para o desenho da medição:** medir FP com `cpu_by_pod` **subestima** o
   valor do FDR vs produção. Uma medição production-representativa deve usar as 18 regras
   tunadas do gotmpl como ambiente.

**Pendente (decisão de julgamento, candidata ao Fable):**
- Re-medir com o rule set de produção para o número de FP representativo.
- Veredito sobre Decision 8 ("detector é commodity") e o critério de recall/FP para
  exit-dry-run. NÃO decidido aqui — só a evidência está registrada.
