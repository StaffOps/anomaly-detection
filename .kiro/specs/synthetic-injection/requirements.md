# Feature: Synthetic Fault Injection on Replay (Measurement Gate)

## Visão geral

Mede empiricamente a qualidade do detector — **recall lower-bound** (quantas falhas
ele pega) e **FP upper-bound** (quantos alarmes falsos dispara) — **sem precisar de
incidentes históricos rotulados**.

A ideia: pegar janelas históricas **limpas** (sem incidente conhecido) de
VictoriaMetrics/Loki, **injetar falhas sintéticas** de forma controlada (a falha é o
ground truth, porque nós sabemos exatamente onde e quando a injetamos), rodar o
detector via replay sobre os dados perturbados, e comparar o que ele detectou contra a
verdade injetada.

Isto é o **P0.1 — measurement gate** do roadmap. É o pré-requisito que destrava toda
decisão algorítmica: sem este número, trocar o detector (Holt-Winters, multivariado) é
fé, não engenharia. Também é o experimento que confirma ou refuta empiricamente a
Decision 8 (`docs/architecture/decisions.md`: "o detector univariado é commodity").

> **Por que isto é honesto e não circular**: a falha sintética é a verdade. Não
> dependemos de ninguém lembrar de um incidente, nem de rótulos subjetivos. Se
> injetamos uma rampa de erro de 10min na série X e o detector não a pega, isso é um
> false negative objetivo. É a forma mais barata de sair de "não sei se funciona".

## User Stories

### US-1: Injetar falha sintética numa janela limpa

WHEN o operador roda replay com um perfil de injeção (`--inject=<perfil>`)
THEN o sistema SHALL perturbar as séries-alvo especificadas, na janela temporal
especificada, com o tipo de falha especificado (spike/ramp/step/silence)
AND o sistema SHALL registrar a janela de injeção como **ground truth** (série, início,
fim, tipo).

### US-2: Tipos de falha mapeados ao modelo de degradação

WHEN o operador escolhe um tipo de falha
THEN o sistema SHALL suportar pelo menos: `spike` (transiente curto), `ramp`
(crescimento gradual — o caso onde EWMA persegue), `step` (mudança de nível
súbita/sustentada), `silence` (série some — testa detecção de ausência de sinal)
AND cada tipo SHALL ser parametrizável (magnitude, duração).

### US-3: Scoring contra ground truth

WHEN o replay termina com injeção ativa
THEN o sistema SHALL classificar cada anomalia detectada como TP/FP e cada janela de
injeção como detectada/perdida (FN)
AND o sistema SHALL computar **precision, recall, F1**
AND o sistema SHALL reportar **detection latency** (tempo entre o início da falha
injetada e a primeira detecção dentro da janela).

### US-4: Breakdown por tipo de falha

WHEN o scoring é computado
THEN o sistema SHALL quebrar recall **por tipo de falha** (spike vs ramp vs step vs
silence)
AND o sistema SHALL tornar visível se o recall de `ramp` é sistematicamente pior que o
de `spike` (a hipótese do round 4: o detector é cego à subida de rampa).

### US-5: FP baseline (injeção vazia)

WHEN o operador roda replay com `--inject=none` sobre uma janela limpa
THEN o sistema SHALL reportar todas as detecções como **false positives** (não há
falha real)
AND isto SHALL dar o **FP upper-bound** sobre dados limpos — o "ruído de base" do
detector.

### US-6: Zero efeito colateral (herda do replay)

WHEN qualquer injeção roda
THEN o sistema SHALL manter as invariantes do replay: sem Redis, sem Alertmanager, sem
gRPC workers, sem ML
AND a injeção SHALL acontecer **apenas em memória**, nunca escrevendo nos datasources.

### US-7: Reprodutibilidade

WHEN o operador roda a mesma injeção com a mesma seed sobre a mesma janela
THEN o resultado SHALL ser determinístico (mesma seed → mesmas perturbações → mesmo
score).

## Acceptance Criteria

- [ ] Injeção perturba séries reais em memória entre `QueryRange` e detecção — datasources nunca modificados
- [ ] 4 tipos de falha: spike, ramp, step, silence — todos parametrizáveis (magnitude, duração)
- [ ] Ground truth registrado por injeção (série-alvo, início, fim, tipo, magnitude)
- [ ] Scoring computa precision, recall, F1 contra ground truth
- [ ] Detection latency reportada por janela de falha detectada
- [ ] Recall quebrado por tipo de falha (expõe a cegueira-a-rampa se existir)
- [ ] `--inject=none` sobre janela limpa dá FP upper-bound (todas detecções = FP)
- [ ] Resultado determinístico com seed fixa
- [ ] Herda todas as invariantes do replay (sem side effects)
- [ ] Output JSON estende o schema do replay com bloco `injection` + `scoring`
- [ ] Testes unitários ≥90% no novo código de injeção + scoring
- [ ] Documentado: como rodar, como ler o número, o que ele NÃO prova

## Fora de escopo (V1)

- **Injeção correlacionada multi-série** (latência↑ + erro↑ + throughput↓ juntos,
  seguindo uma cadeia do degradation-model) — V1 injeta por série independente. A
  injeção causal-realista é V2 e depende da direção do produto ser confirmada.
- **Injeção em logs (Loki)** — V1 foca em métricas (VM). Logs são mais difíceis de
  perturbar realisticamente (texto, não série numérica). V2.
- **Geração de séries 100% sintéticas** (sem base real) — V1 perturba séries reais
  limpas, que carregam realismo de ruído/sazonalidade. Sintético puro é menos
  realista e fica fora.
- **Auto-seleção de janelas limpas** — V1 o operador escolhe a janela (e declara que é
  limpa). Detectar automaticamente "janela sem incidente" é circular (usaria o
  detector que estamos avaliando). V2 poderia usar ausência de alertas históricos como
  proxy.
- **Tuning automático de thresholds a partir do score** — V1 só mede. Otimização é
  trabalho posterior, gated neste número.
- **ML no scoring** — herda a exclusão de ML do replay V1.

## Precondições

- **Replay mode funcional** (✅ P3.1 DONE) — esta feature estende o replay existente.
- Acesso de leitura a VM/Loki históricos (já usado pelo replay).
- Janelas limpas conhecidas — responsabilidade do operador declarar (ver Fora de
  escopo: auto-seleção é V2).

## O que este número prova e o que NÃO prova

**Prova**:
- Lower-bound de recall: se o detector não pega falhas sintéticas óbvias, não vai pegar
  reais. Falha aqui é falha objetiva.
- Upper-bound de FP sobre dados limpos: o ruído de base do detector.
- A forma da fraqueza: se `ramp` recall ≪ `spike` recall, confirma a cegueira-a-rampa.

**NÃO prova**:
- Que falhas sintéticas representam falhas reais fielmente (são aproximações — por isso
  recall medido é *lower-bound*: falhas reais podem ser mais sutis).
- Performance em incidentes multivariados correlacionados (V1 é por-série).
- Que o detector é "bom o suficiente" em absoluto — só dá o número pra decidir.
