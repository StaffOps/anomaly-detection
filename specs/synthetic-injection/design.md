# Design: Synthetic Fault Injection on Replay

## Arquitetura

A injeção é uma **camada de transformação** inserida no loop de replay existente,
**entre** a query de dados reais (`vm.QueryRange`) e a extração de samples por tick
(`SamplesAt`). Ela perturba as `TimeSeries` em memória de acordo com um perfil de
injeção, e registra exatamente o que perturbou (ground truth). O scoring roda no fim,
comparando o `Report` do replay contra o ground truth.

```
┌─────────────────────────────────────────────────────────────────┐
│  controller --replay --inject=ramp.yaml --from=48h --to=1h        │
└───────────────────────────────┬───────────────────────────────────┘
                                │
                  ┌─────────────▼──────────────┐
                  │  replay.Run (existing loop)  │
                  │                             │
                  │  per tick:                  │
                  │   vm.QueryRange ──► []TimeSeries (REAL, clean)  │
                  │            │                │
                  │   ┌────────▼─────────┐      │   NEW
                  │   │ Injector.Apply   │◄─────┼── injection profile
                  │   │ (perturb series  │      │   + ground truth log
                  │   │  in memory)      │      │
                  │   └────────┬─────────┘      │
                  │            │                │
                  │   SamplesAt(tick) ──► detection.Engine (UNCHANGED) │
                  │            │                │
                  │   addAll(rb, anomalies)     │
                  └────────────┬────────────────┘
                               │
                  ┌────────────▼─────────────┐
                  │  Scorer.Score             │  NEW
                  │  Report.anomalies         │
                  │     vs GroundTruth        │
                  │  → precision/recall/F1    │
                  │  → latency, per-type      │
                  └────────────┬──────────────┘
                               │
                  ┌────────────▼─────────────┐
                  │  Report + InjectionResult │
                  │  → JSON (extends replay)  │
                  └───────────────────────────┘
```

## Componentes

| Componente | Responsabilidade | Localização |
|-----------|-----------------|-------------|
| `InjectionConfig` | Parse do perfil de injeção (YAML): alvos, tipo, janela, magnitude, seed | `internal/replay/inject/config.go` |
| `Injector` | Aplica perturbação a `[]ingestion.TimeSeries` em memória; emite `GroundTruth` | `internal/replay/inject/injector.go` |
| `faultFunc` | Funções de perturbação por tipo: spike, ramp, step, silence | `internal/replay/inject/faults.go` |
| `GroundTruth` | Registro do que foi injetado (série, intervalo, tipo, magnitude) | `internal/replay/inject/groundtruth.go` |
| `Scorer` | Compara anomalias detectadas vs ground truth → métricas | `internal/replay/inject/scorer.go` |
| Hook em `replay.Run` | Chamar `Injector.Apply` após `QueryRange`, antes de `SamplesAt`; chamar `Scorer` no fim | `internal/replay/engine.go` (extensão mínima) |

## Ponto de inserção (por que aqui)

O loop atual (`engine.go`) faz, por tick:
```go
series, err := vm.QueryRange(ctx, am.Query, chunkStart, tick, step)  // dados reais limpos
// ... acumula metricSeries ...
samples := SamplesAt(tick, metricSeries)                             // extrai ponto do tick
anomalies := engine.EvaluateMetricsAdaptive(ctx, am.Name, samples)   // detecta
```

A injeção entra **entre `QueryRange` e `SamplesAt`**:
```go
series, err := vm.QueryRange(...)              // real, limpo
series = injector.Apply(am.Name, series)       // NEW: perturba em memória (no-op se sem perfil)
metricSeries = append(metricSeries, series...)
```

Por que aqui e não antes (no datasource) ou depois (nos samples):
- **Antes (no VM)**: violaria a invariante "não escreve em datasource" e poluiria
  dados reais. Inaceitável.
- **Depois (no sample já extraído)**: perderia a forma temporal — uma rampa precisa
  perturbar *a série inteira ao longo do tempo*, não um ponto. Injetar no `TimeSeries`
  preserva a dinâmica temporal que o detector adaptativo realmente vê.
- **Aqui**: o detector recebe exatamente a série perturbada como se fosse real. Zero
  divergência de caminho — mesma `EvaluateMetricsAdaptive` de produção.

`Injector.Apply` é **no-op quando não há perfil** → replay normal inalterado.

## Tipos de falha (faultFunc)

Cada `faultFunc` recebe a série real e a janela de injeção `[start, end]`, retorna a
série perturbada. Determinístico dado seed.

| Tipo | Perturbação | Testa o quê |
|------|-------------|-------------|
| `spike` | Multiplica/soma um pico em pontos dentro de `[start, end]` curta (ex: 1-2 min) | Detecção de transiente — caso fácil, sanity check |
| `ramp` | Cresce linearmente de 0 a `magnitude` ao longo de `[start, end]` (ex: 10-30 min) | **O caso crítico**: EWMA persegue rampa → expõe cegueira-a-rampa |
| `step` | Salto súbito para novo nível em `start`, sustentado até `end` | Mudança de nível (deploy-like): detecta uma vez ou re-baselina? |
| `silence` | Remove pontos em `[start, end]` (série some) | Detecção de ausência de sinal — hoje o detector é cego (ver threat-model) |

Parâmetros comuns no perfil: `magnitude` (em múltiplos de stddev da série ou valor
absoluto), `duration`, `target` (nome da métrica + matcher de labels), `seed`.

### Nota sobre `silence`

O detector atual **não** detecta ausência de sinal (confirmado no código — z-score
sobre valor presente; sem dado = sem avaliação). Espera-se que `silence` tenha
**recall ~0** em V1. Isso é um **resultado válido e desejado**: quantifica
objetivamente um furo já conhecido, e dá baseline pra quando P2.10 (dead-man's-switch)
for implementado. Não é bug do harness.

## Ground truth e scoring

### GroundTruth

```go
type GroundTruth struct {
    Target    string        // metric name + label fingerprint
    Type      string        // spike|ramp|step|silence
    Start     time.Time
    End       time.Time
    Magnitude float64
}
```

Uma run pode ter múltiplas injeções (várias séries, vários tipos).

### Matching anomalia ↔ ground truth

Uma anomalia detectada é **TP** se:
- seu `target` (métrica + labels) casa com uma `GroundTruth`, E
- seu timestamp cai dentro de `[start, end + grace]` (grace = 1 tick interval, pra
  detecção logo após o fim da falha).

Caso contrário é **FP**.

Uma `GroundTruth` é **detectada** se ≥1 TP casa com ela; senão é **FN**.

### Métricas

```
precision = TP / (TP + FP)
recall    = detected_truths / total_truths
F1        = 2·precision·recall / (precision + recall)
detection_latency = first_TP.timestamp - groundtruth.start   (por truth detectada)
```

Quebra por tipo: `recall_by_type[spike|ramp|step|silence]`.

### Sobre FP — honestidade do número

Numa janela "limpa", uma detecção fora de qualquer ground truth é contada como FP. Mas
a janela pode conter um incidente real não-rotulado → esse FP pode ser um TP "real" que
não sabemos. Por isso o número é **FP upper-bound**, não FP exato. Documentado no
output e no `requirements.md`. A direção do erro é conservadora (superestima FP), o que
é seguro pra decisão.

## Configuração (perfil de injeção)

```yaml
# inject-ramp.yaml
seed: 42
injections:
  - target:
      metric: error_rate_by_service
      labels: { service_name: "checkout-api" }
    type: ramp
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 5.0   # 5σ acima do baseline da própria série ao fim da rampa
  - target:
      metric: latency_p99_by_service
      labels: { service_name: "checkout-api" }
    type: step
    start: "2026-06-10T03:05:00Z"
    end: "2026-06-10T03:30:00Z"
    magnitude: 3.0
```

CLI: `controller --replay --inject=inject-ramp.yaml --from=... --to=... --output=score.json`
`--inject=none` (ou ausência) → replay normal sem injeção.

## Output (estende o schema do replay)

Adiciona dois blocos ao JSON do replay existente:

```json
{
  "metadata": { "...": "replay metadata (existing)" },
  "totals": { "...": "replay totals (existing)" },
  "anomalies": [ "... (existing)" ],

  "injection": {
    "seed": 42,
    "ground_truths": [
      {"target": "error_rate_by_service{service_name=checkout-api}",
       "type": "ramp", "start": "...", "end": "...", "magnitude": 5.0}
    ]
  },
  "scoring": {
    "precision": 0.0,
    "recall": 0.0,
    "f1": 0.0,
    "tp": 0, "fp": 0, "fn": 0,
    "recall_by_type": {"spike": 0.0, "ramp": 0.0, "step": 0.0, "silence": 0.0},
    "detection_latency_seconds": {"checkout-api/ramp": 0.0},
    "fp_caveat": "FP is an upper-bound: the clean window may contain unlabeled real incidents"
  }
}
```

## Rationale

### Decisão 1: Perturbar séries reais limpas (não gerar séries sintéticas do zero)

**Escolha**: injetar falhas sobre dados históricos reais de VM/Loki, não gerar
timeseries 100% artificiais.

**Justificativa, em ordem de força**:
1. Realismo de ruído e sazonalidade: a série real carrega o jitter, a sazonalidade e a
   forma que o detector enfrenta em prod. Sintético puro testaria o detector contra um
   mundo que não existe — recall otimista demais.
2. O baseline que o detector aprende durante o warm-up é o baseline *real* da série →
   o desvio injetado é medido contra a dispersão real, não uma inventada.
3. Custo zero de modelagem de "como é uma série normal" — já temos milhares.

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| A janela "limpa" pode ter incidente não-rotulado | Torna FP um upper-bound (conservador, seguro pra decisão) |
| Menos controle sobre a forma exata da série base | Aceitável — é o ponto: testar contra o mundo real |

**Quando estaria errada**: se não houvesse janelas limpas suficientes (cluster sempre
em incidente) — aí sintético seria a única opção. Não é o caso.

### Decisão 2: Injetar no `TimeSeries`, não no datasource nem no sample

**Escolha**: ponto de inserção entre `QueryRange` e `SamplesAt`.

**Justificativa**: preserva a dinâmica temporal (essencial pra `ramp`/`step`), mantém a
invariante de não escrever no datasource, e faz o detector rodar o **mesmo caminho** de
produção sobre a série perturbada. As três alternativas (datasource / sample) quebram
uma dessas propriedades — detalhado em "Ponto de inserção" acima.

**Trade-off**: a injeção precisa conhecer o formato `TimeSeries` interno → acopla o
harness à estrutura de ingestão. Aceitável: é código de teste/medição, evolui junto.

### Decisão 3: Scoring separado da detecção (não instrumentar o detector)

**Escolha**: o `Scorer` compara o `Report` final contra o ground truth, post-hoc. O
detector não sabe que está sendo medido.

**Justificativa, em ordem de força**:
1. Mantém o detector idêntico ao de produção — zero risco de "instrumentação muda o
   comportamento medido".
2. O scorer é trocável/evolutível sem tocar no detector.
3. Permite re-score do mesmo `Report` com regras de matching diferentes (ex: ajustar
   grace window) sem re-rodar o replay (caro).

**Trade-off**: o matching anomalia↔truth depende de `target` (métrica+labels) bater
exatamente. Labels inconsistentes quebram o match. Mitigação: fingerprint de labels
normalizado, testado.

## Invariantes

- Injeção acontece **apenas em memória** — VM/Loki nunca modificados
- Herda todas as invariantes do replay (sem Redis/Alertmanager/gRPC/ML)
- `Injector.Apply` sem perfil é **no-op exato** — replay normal não muda em nada
- Mesma seed + mesma janela + mesmo perfil = mesmo score (determinístico)
- O detector roda o **mesmo caminho** de produção sobre a série perturbada (sem branch
  especial "modo medição")
- FP reportado é sempre **upper-bound** (documentado, nunca apresentado como exato)
- `silence` com recall ~0 é resultado esperado em V1, não falha do harness

## Dependências externas

| Serviço | Propósito | Operação |
|---------|-----------|----------|
| Prometheus-compatible TSDB | Séries reais limpas (base da injeção) | `GET /api/v1/query_range` (herda do replay) |
| Loki | (V2 — injeção em logs fora de escopo V1) | — |
| Redis/Alertmanager/Workers/ML | **NÃO acessados** | — |

## Riscos e mitigações

| Risco | Probabilidade | Mitigação |
|-------|--------------|-----------|
| Janela "limpa" tem incidente real → infla FP | média | FP é upper-bound por design; operador escolhe janelas com baixa atividade de alerta histórica |
| Magnitude irreal (5σ) gera recall otimista | média | Variar magnitude (2σ, 3σ, 5σ); reportar recall por magnitude em runs separadas |
| Match anomalia↔truth falha por label mismatch | média | Fingerprint normalizado + teste unitário com labels reais |
| `ramp` muito curta não expõe cegueira-EWMA | baixa | Documentar duração mínima recomendada (≥ several tick intervals) |
| Operador interpreta recall como absoluto | média | Output e doc deixam explícito: lower-bound, sintético, por-série |
