# Design: Replay Mode

## Arquitetura

```
┌─────────────────────────────────────────────────────────────┐
│  controller --replay --from=24h --to=1h --config=cand.yaml  │
└──────────────────────────┬──────────────────────────────────┘
                           │
                  ┌────────▼─────────┐
                  │  ReplayEngine     │   (new: internal/replay/)
                  │  - parse window   │
                  │  - tick simulator │
                  │  - in-mem baseln  │
                  │  - report builder │
                  └────────┬──────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
   ┌────▼──────┐   ┌──────▼─────┐   ┌────────▼──────┐
   │  VM range │   │ Loki range │   │ detection.    │
   │  query    │   │ query      │   │ Engine        │
   │  /api/v1/ │   │ /loki/api/ │   │ (REUSED from  │
   │  query_   │   │ v1/query_  │   │  prod path)   │
   │  range    │   │ range      │   │               │
   └────┬──────┘   └─────┬──────┘   └────────┬──────┘
        │                │                    │
        └────────┬───────┴────────────────────┘
                 │
        ┌────────▼───────┐
        │  Detect cycle  │ (per simulated tick)
        │  - static      │
        │  - adaptive    │
        │  - log rate    │
        └────────┬───────┘
                 │
        ┌────────▼───────┐
        │  ReplayReport  │
        │  (in-memory)   │
        └────────┬───────┘
                 │
        ┌────────▼─────────────────────┐
        │  output.json + output.md     │
        └──────────────────────────────┘
```

## Componentes

| Componente | Responsabilidade | Onde |
|-----------|------------------|------|
| `cmd/controller/main.go` | Detecta `--replay` flag, delega para ReplayEngine ao invés de loop normal | existing |
| `internal/replay/engine.go` | Orquestra: parse window, fan out queries, simula ciclos | NEW |
| `internal/replay/window.go` | Parse de `--from`/`--to` (durações relativas e timestamps absolutos) | NEW |
| `internal/replay/inmem_baseline.go` | Implementação in-memory de `baseline.Store` (mesma interface, sem Redis) | NEW |
| `internal/replay/report.go` | Estrutura do relatório + serializadores JSON/Markdown | NEW |
| `internal/ingestion/metrics.go` | **Adicionar** `QueryRange(ctx, query, start, end, step)` reusando `MetricsPoller` | extend |
| `internal/ingestion/logs.go` | **Adicionar** `QueryMetricRange(...)` análogo | extend |
| `internal/detection/*` | **Reuse** sem modificação — replay usa mesmo Engine | reuse |

## Decisões de design

### 1. Modo replay como flag, não subcommand

**Por que**: replay reusa 90% do código do controller (config loading, detection engine, suppression filter, correlator, etc.). Subcommand separado obrigaria duplicação. Flag `--replay` apenas troca o loop principal.

```go
if *replay {
    runReplay(cfg, fromTime, toTime, outputPath)
    return
}
// else: normal cycle loop
```

### 2. Baseline in-memory em vez de Redis

**Por que**: replay não pode poluir o estado de prod. Implementar `baseline.Store` interface in-memory, montada do zero a cada execução. Determinístico, isolado.

```go
type InMemStore struct {
    mu     sync.Mutex
    series map[string]*baseline.Stats
}
// implements baseline.Store interface (Evaluate, etc.)
```

### 3. Warm-up split (dinâmico)

**Por que**: detector adaptive (Z-score) precisa de N amostras antes de fazer detecção confiável. Em prod isso vem da história em Redis. Em replay, dividimos a janela:
- **Warm-up phase**: alimenta baselines, `IsWarmingUp=true` filtra anomalies (mesma lógica de prod)
- **Detection phase**: emite anomalies normalmente

**Fórmula**:
```
warmup_duration = max(0.2 × window, baseline.warm_up_samples × tick_interval)
```

Para defaults `warm_up_samples=60` e `tick_interval=30s`:
- Janela 24h → warmup = max(4.8h, 30min) = **4.8h** (limitado pela fração)
- Janela 1h → warmup = max(12min, 30min) = **30min** (limitado pelo mínimo absoluto)
- Janela 30min → warmup = max(6min, 30min) = 30min, mas 30min == janela → **falha rápido**

**Mínimo absoluto**: replay falha com mensagem clara se `window < (warmup_min / 0.2) = 2.5h`. Operador é informado de quanto precisa abrir a janela.

**IsWarmingUp filter**: detector adaptive já honra `Result.IsWarmingUp` em produção. Replay reusa esse filtro sem mudança — durante warmup phase, baselines são alimentadas mas anomalies não são emitidas. Detection phase começa quando `count >= warm_up_samples`.

Configurável via flag `--warmup-fraction=0.2`.

### 4. Tick simulator

Em prod o ciclo roda a cada `controller.job_interval` (default 30s). No replay, simulamos:
- Para cada tick `T` em `[from + warmup, to]` step `job_interval`:
  - Faz queries range cobrindo `[T - lookback, T]` para cada metric
  - Roda detection engine normalmente
  - Acumula anomalies no relatório

Lookback é o mesmo dos queries de produção (1m default, 5m para histogramas). Não há diferença comportamental.

### 5. Range queries vs instant queries

Produção usa `/api/v1/query` (instant). Replay precisa `/api/v1/query_range` (timeseries). Solução:
- Adicionar método `QueryRange` no `ingestion.MetricsPoller`
- Replay pré-carrega ranges em chunks de 1h
- Adaptador converte um sample do range no equivalente "instant at T"

Trade-off: mais memória (carrega chunk de 1h na RAM), mas evita N queries instant por tick (que seria N×120 = 14.400 queries para 1h replay). Bem mais rápido.

### 6. ML em V1: explicitamente NÃO chamado

ML Isolation Forest é stateful — em replay precisa cuidado. **V1 nunca chama ML.** Razão: chamar o ML server de produção alimenta o Isolation Forest com dados de replay, poluindo o histórico do modelo de prod (replay teoricamente roda over-and-over com mesmas janelas, distorcendo aprendizado).

**Workaround documentado** (não implementado V1): operador que precisa replay com ML pode subir uma segunda instância do ML server isolada e setar `--ml-endpoint=replay-ml:50051`. V2 adicionará suporte de primeira classe via `--ml=true` que automaticamente isola estado.

Por ora, replay roda só com static + adaptive + log detection. ML é zero no output. Documentado como TODO no `--help`.

### 7. Output

Todo timestamp em **UTC**. Operador converte localmente se quiser (auto-detect TZ é confuso).

**JSON**:
```json
{
  "metadata": {
    "schema_version": "1",
    "controller_version": "0.7.0",
    "ran_at": "2026-05-30T20:00:00Z",
    "window_start": "2026-05-29T20:00:00Z",
    "window_end": "2026-05-30T20:00:00Z",
    "warmup_start": "2026-05-29T20:00:00Z",
    "warmup_end": "2026-05-30T00:48:00Z",
    "warmup_fraction": 0.2,
    "tick_interval_seconds": 30,
    "result_status": "anomalies_detected",
    "config_summary": {"static_rules": 3, "adaptive_metrics": 4, "log_patterns": 3},
    "execution_metrics": {
      "duration_seconds": 187.4,
      "ticks_processed": 2304,
      "ticks_skipped_query_error": 12,
      "vm_queries_total": 4608,
      "vm_query_duration_seconds_p95": 0.42,
      "loki_queries_total": 1152,
      "memory_peak_mb": 348
    }
  },
  "totals": {
    "anomalies": 542,
    "by_severity": {"warning": 489, "critical": 53},
    "by_signal": {"metrics": 410, "logs": 132},
    "by_detector": {"static": 87, "adaptive": 455},
    "by_kind": {"pod": 530, "workload": 12},
    "warmup_skipped": 0,
    "query_errors": 12
  },
  "top_workloads": [
    {"namespace": "x", "workload": "y", "count": 32}
  ],
  "timeline": [
    {"hour": "2026-05-30T01:00:00Z", "anomalies": 18, "by_severity": {"warning": 16, "critical": 2}}
  ],
  "anomalies": [
    {"timestamp": "2026-05-30T01:14:30Z", "namespace": "x", "pod": "y", "metric": "cpu_by_workload", "severity": "warning", "score": 4.2, "current": 0.87, "baseline_mean": 0.42}
  ]
}
```

**`result_status`**:
- `"anomalies_detected"` — ≥1 anomaly detectada, replay normal
- `"no_anomalies"` — replay rodou sem erros mas zero anomalies (resultado válido)
- `"partial"` — `query_errors > 0` (algum tick foi skipped)

**Markdown**: derivado do JSON, com tabelas e gráficos ASCII opcionais (sparklines de timeline). Adequado pra colar em PR description. Timezone também UTC.

## Error handling

Princípio: **replay parcial > replay abortado**. Operador prefere relatório com 95% dos ticks processados a um erro genérico.

### Erros pré-replay (fail fast)

Antes de começar o tick simulator, validar:

| Cenário | Comportamento |
|---------|--------------|
| `from > to` ou janela < 2.5h | Erro claro, exit non-zero |
| `to - from > max_range` (default 7d) | Erro claro com sugestão de `--max-range` |
| VM `/api/v1/query?query=up` falha | Erro: "VM unreachable, refusing to start replay" |
| Loki `/loki/api/v1/labels` falha | Erro: "Loki unreachable, refusing to start replay" |
| Config inválido (parse error) | Erro: linha + mensagem |
| Output path inacessível (write protected) | Erro antes de processar qualquer tick |

### Erros durante replay (graceful degrade)

Se o tick simulator está rodando e algo falha:

| Cenário | Comportamento |
|---------|--------------|
| Query VM retorna 500/timeout em tick T | Skip tick T, log warning, incrementa `ticks_skipped_query_error`. Próximo tick prossegue. |
| Query Loki retorna 500/timeout em tick T | Skip apenas a parte de logs do tick. Métricas do tick continuam. |
| Detection engine panics em tick T | Recover, log error com stack trace, skip tick. |
| Out of memory | Não pode recuperar — abort com mensagem específica + sugestão de janela menor |
| Operador interrompe (`SIGINT`/`SIGTERM`) | Flush relatório parcial até o último tick processado, marca `result_status: partial`, exit 0 |

### Reporting

`metadata.execution_metrics.ticks_skipped_query_error` conta quantos ticks foram skipped. Se > 0, `result_status = "partial"`. Operador vê no markdown: "⚠️ 12 ticks skipped due to query errors (X% of total)".

### Limites e proteções

| Limite | Default | Comportamento ao bater |
|--------|---------|------------------------|
| Memory cap | 1 GB peak | Abort, mensagem clara |
| Timeout total | 30 min para janela 7d | Soft warning aos 25min, abort aos 30min |
| Max series por query range | 5000 | Truncar com warning, marcar `top_n_truncated: true` na metadata |
| Max anomalies no JSON | 1000 (configurable) | Truncar com warning |

## Invariantes

- Replay **não escreve em Redis**, **não chama Alertmanager**, **não chama workers via gRPC**
- Mesmo código de detecção que produção (zero divergência comportamental sobre dados conhecidos)
- Warm-up é determinístico — mesma janela + mesma config = mesmo resultado
- Output JSON é estável (schema versionado em `output.metadata.schema_version`)
- Falha rápida em qualquer condição inesperada — não silenciosa
- Logs em modo replay são claramente prefixados com `replay=true` ou usam logger separado

## Dependências externas

| Serviço | Propósito | Operação |
|---------|-----------|----------|
| Prometheus-compatible TSDB | Histórico de métricas | `GET /api/v1/query_range` |
| Loki | Histórico de logs | `GET /loki/api/v1/query_range` |
| Redis | — | **NÃO acessado em replay** |
| Alertmanager | — | **NÃO acessado em replay** |
| Workers (gRPC) | — | **NÃO acessado em replay** |

## Métricas (separadas de prod)

- `staffops_ad_replay_runs_total` — total de execuções de replay
- `staffops_ad_replay_duration_seconds` — duração total
- `staffops_ad_replay_query_duration_seconds{datasource}` — latência das range queries
- `staffops_ad_replay_anomalies_total{detector,severity}` — anomalias detectadas no último replay (Gauge, reset a cada run)

Não incrementam contadores de produção (`staffops_ad_detection_anomalies_total` etc.).

## Riscos e mitigações

| Risco | Probabilidade | Mitigação |
|-------|--------------|-----------|
| VM range query timeout em janelas longas | alta | Chunk em janelas de 1h, retry com backoff |
| Memory blowup carregando muitas séries | média | Hard cap de séries por query (top N por workload, configurable) |
| Replay diverge da prod por bug em range→instant adapter | média | Integration test com data sintética conhecida |
| Operador roda replay em prod stack atrapalhando perf de VM | baixa | Logar warning quando replay roda em janela curta de horário pico |
| Output JSON cresce demais (5MB+) | média | Truncar lista de anomalies em N (default 1000), expor flag `--max-anomalies` |

## Sequence diagram (caminho feliz, simplificado)

```
operator              controller(--replay)        VM            Loki
   │                        │                      │             │
   │── ./controller ─────────►                      │             │
   │   --replay              │                      │             │
   │   --from=24h            │                      │             │
   │                        │                      │             │
   │                  ┌─────┤ parse window           │             │
   │                  │     │ build in-mem store     │             │
   │                  └────►│                       │             │
   │                        │                      │             │
   │                        ├──── query_range ─────►│             │
   │                        │   (warmup chunks)     │             │
   │                        │◄────samples──────────┤             │
   │                        │                      │             │
   │                        ├──── query_range ──────────────────►│
   │                        │   (logs, warmup)       │             │
   │                        │◄────samples────────────────────────┤
   │                        │                      │             │
   │                  ┌─────┤ feed baselines         │             │
   │                  │     │ (no anomalies emitted) │             │
   │                  │     │                       │             │
   │                  │     │ tick T = warmup_end:   │             │
   │                  │     │   detect → record       │             │
   │                  │     │ tick T += interval:    │             │
   │                  │     │   detect → record       │             │
   │                  │     │ ... (iterate)           │             │
   │                  └────►│                       │             │
   │                        │                      │             │
   │                  ┌─────┤ build report           │             │
   │                  │     │ write JSON + MD         │             │
   │                  └────►│                       │             │
   │◄─── output saved ──────┤                       │             │
   │     report.json         │                      │             │
   │     report.md           │                      │             │
```

## Open questions (resolvidas após review)

- [x] **Range query chunk size**: 1h fixo. Simplicidade > otimização especulativa.
- [x] **Output path default**: `./replay-report.json` (sobrescreve). Operador faz histórico via shell se quiser.
- [x] **`--config-diff=base.yaml` mostrando diff entre prod e candidata**: V2.
- [x] **Replay deve respeitar `suppression`**: Sim ✅
- [x] **Workload-pattern detection (P2.4) ativo no replay**: Sim ✅
- [x] **Métricas Prometheus exposure**: V1 sem exposure (registry separado, embedadas no JSON output). V2 sob demanda.
- [x] **ML em replay**: V1 explicitamente NÃO chama. V2 com instance isolada.
- [x] **Timezone**: UTC sempre, em ambos JSON e Markdown.
- [x] **Resultado vazio**: válido, marcado como `result_status: no_anomalies`.
- [x] **Erro de query mid-replay**: skip tick, não aborta. Marca `result_status: partial`.
- [x] **Cache de queries pra iterar config**: V2.
