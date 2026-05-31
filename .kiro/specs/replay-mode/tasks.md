# Tasks: Replay Mode

Sequenciamento das tasks com dependências explícitas. Cada task entrega valor (commit verde, build OK, testes passando) ou é claramente plumbing pra próxima.

## Phase 1 — Foundation (range queries + window parsing)

- [x] **T1 — Window parser**
  - File: `controller/internal/replay/window.go`
  - Função: `ParseWindow(fromStr, toStr string, maxRange time.Duration) (time.Time, time.Time, error)`
  - Aceita: `24h`, `30m`, `2026-05-30T00:00:00Z`, mistura
  - Default `--to=now` quando ausente
  - Validações: from < to, to <= now, range mínimo (2.5h — derivado de warm-up min), range máximo (default 7d, configurable)
  - **Sempre converte para UTC** (`time.UTC()`)
  - Unit tests cobrindo todos os casos + edge cases (DST, leap second irrelevante, future timestamps)

- [x] **T2 — VM range query**
  - File: `controller/internal/ingestion/metrics.go`
  - Adicionar método `QueryRange(ctx, query string, start, end time.Time, step time.Duration) ([]TimeSeries, error)`
  - `TimeSeries` é `{Labels map[string]string, Points []Point}`, `Point` é `{T time.Time, V float64}`
  - Reusa `MetricsPoller.client` e error handling existente
  - Unit test com mock HTTP server retornando promResponse range

- [x] **T3 — Loki range query**
  - File: `controller/internal/ingestion/logs.go`
  - Adicionar `QueryMetricRange(ctx, query, start, end, step) ([]TimeSeries, error)` análogo a T2
  - Mesmo formato `TimeSeries`
  - Unit test similar

## Phase 2 — In-memory baseline store

- [x] **T4 — InMemStore**
  - File: `controller/internal/replay/inmem_baseline.go`
  - Implementa `baseline.Store` interface (mesmo método `Evaluate(ctx, metric, labels, value) (*Result, error)`)
  - State: `map[string]*Stats` em memória, mutex protegido
  - Mesma matemática (Welford EWMA) — copia ou refatora para shared package
  - Bench test garantindo perf razoável (>100k Evaluate/sec single thread)
  - **Depends**: nenhuma

- [x] **T5 — Refactor baseline.Store interface (se preciso)**
  - Se `baseline.Store` ainda é struct concreta, extrair interface mínima (`Evaluate(...)`)
  - Atualizar `detection/adaptive.go` para receber interface, não struct
  - Sem mudança comportamental — só introdução de interface
  - **Depends**: T4 (sabe-se até onde a interface precisa ir)

## Phase 3 — Replay engine + tick simulator

- [x] **T6 — ReplayConfig parsing**
  - File: `controller/internal/replay/config.go`
  - Struct `ReplayConfig { From, To time.Time, ConfigPath, OutputPath string, WarmupFraction float64, MaxAnomalies int, EnableEnrichment, EnableML bool }`
  - Default values
  - Loaded from CLI flags em `cmd/controller/main.go`
  - **Depends**: T1

- [x] **T7 — Tick simulator**
  - File: `controller/internal/replay/engine.go`
  - Função `Run(ctx, ReplayConfig, *config.Config) (*Report, error)`
  - Carrega chunks de 1h via VM/Loki range queries (T2+T3)
  - Constrói InMemStore do warm-up (warm-up duration = `max(0.2 × window, baseline.warm_up_samples × tick_interval)`)
  - Itera ticks `[warmup_end, to]` step `JobInterval`, chama `detection.Engine` (existente!) com samples adaptados de range para "instant at T"
  - Detector adaptive honra `IsWarmingUp` (filtra anomalies durante warmup phase)
  - Acumula anomalies em `Report` (in-memory)
  - **Error handling**: se query falha em tick T → log warn, incrementa `ticks_skipped_query_error`, próximo tick continua. Replay nunca aborta por query error mid-run.
  - **SIGTERM/SIGINT**: flush relatório parcial até último tick, marca `partial`, exit 0
  - Loga progresso cada 10% da janela
  - **Depends**: T2, T3, T4, T6

- [x] **T8 — Range to instant adapter**
  - File: `controller/internal/replay/adapter.go` (ou inline em engine.go)
  - Helper `samplesAt(ts time.Time, series []TimeSeries) []ingestion.Sample`
  - Pega o último ponto antes/igual `ts` de cada série e retorna como `ingestion.Sample` (compat com `detection.Engine`)
  - Unit test
  - **Depends**: T2 (TimeSeries tipo)

## Phase 4 — Output

- [x] **T9 — Report struct + JSON serializer**
  - File: `controller/internal/replay/report.go`
  - Tipos: `Report`, `Metadata`, `Totals`, `WorkloadCount`, `TimelineEntry`, `AnomalyEntry`, `ExecutionMetrics`
  - Method `WriteJSON(w io.Writer) error`
  - Schema version em metadata (`schema_version: "1"`)
  - Campos novos:
    - `metadata.result_status`: `anomalies_detected` | `no_anomalies` | `partial`
    - `metadata.execution_metrics`: `duration_seconds`, `ticks_processed`, `ticks_skipped_query_error`, `vm_queries_total`, `vm_query_duration_seconds_p95`, `loki_queries_total`, `memory_peak_mb`
    - `totals.query_errors`
    - `totals.by_kind` (pod vs workload, do P2.4)
  - **Timezone**: UTC always (use `time.Time.UTC()` em todo timestamp)
  - Unit test golden file
  - **Depends**: nenhuma estrutural; T7 popula

- [x] **T10 — Markdown serializer**
  - File: `controller/internal/replay/report_md.go`
  - Método `WriteMarkdown(w io.Writer) error`
  - Tabelas: totals, top workloads, distribuição por severity/detector, timeline com ASCII sparklines simples (▁▂▃▄▅▆▇█)
  - Sufixo `.md` automático no output path se `--output=foo.json` → também grava `foo.md`
  - Unit test golden file
  - **Depends**: T9

## Phase 5 — Wire CLI + replay metrics

- [x] **T11 — CLI flags + dispatch**
  - File: `controller/cmd/controller/main.go`
  - Adicionar flags: `--replay`, `--from`, `--to`, `--output` (default `./replay-report.json`), `--warmup-fraction` (default 0.2), `--max-range` (default 7d), `--max-anomalies` (default 1000), `--enrich` (placeholder, V2)
  - **NOT adicionar `--ml`** — V1 explicitamente não suporta. Documentar em `--help` que ML é V2.
  - Pre-flight checks: VM `query=up` OK, Loki labels OK, output path writable. Falha rápido se algum não passa.
  - Se `--replay`: carrega config, monta `ReplayEngine`, executa, sai. Skip Redis/AM/worker setup.
  - Logger separado (`mode=replay`, prefix `[REPLAY]`) para diferenciar de prod
  - Loga banner "REPLAY MODE — no side effects" + janela computada + warmup_end no início
  - **Depends**: T6, T7

- [x] **T12 — Replay-specific metrics (in-memory only)**
  - File: `controller/internal/replay/metrics.go`
  - Tipos: `ExecutionMetrics` struct (counters acumulados durante o run)
  - Campos: `TicksProcessed`, `TicksSkippedQueryError`, `VMQueriesTotal`, `VMQueryDurationP95`, `LokiQueriesTotal`, `MemoryPeakMB`, `DurationSeconds`
  - **NÃO usar Prometheus registry** — V1 sem exposure. Métricas embedadas no JSON output (em `metadata.execution_metrics`).
  - Helper para coletar p95 simples (sliding window de 100 últimas durations, take percentile)
  - **Depends**: nenhuma

## Phase 6 — Integration test e validação

- [ ] **T13 — Integration test docker-compose**
  - File: `controller/internal/replay/replay_integ_test.go` (build tag `integration`)
  - Setup: docker-compose stack rodando (VM + Loki) em modo de teste com dados injetados
  - Cenário: injetar 100 amostras anômalas conhecidas em VM, rodar replay sobre janela contendo essas amostras, verificar que detector pega ≥ 90 delas
  - Skip se `INTEGRATION=1` não setado
  - **Depends**: T11

- [ ] **T14 — Manual smoke test contra prod**
  - Rodar `controller --replay --from=1h --to=now --config=controller/config.yaml --output=/tmp/r.json`
  - Validar manualmente:
    - JSON estrutura correta
    - Markdown legível
    - Total de anomalies "razoável" (mesma ordem do que vemos em prod no `ALARMs.md`)
    - Sem write em Redis (`redis-cli MONITOR` durante replay)
  - Documentar no `ALARMs.md` o resultado
  - **Depends**: T13

## Phase 7 — Docs + sign-off

- [ ] **T15 — README do feature**
  - Adicionar seção `## Replay Mode` em `controller/README.md`
  - Comando exemplo, output samples, casos de uso
  - **Depends**: T14

- [ ] **T16 — Atualizar ROADMAP**
  - Mover P3.1 para Done section
  - Documentar resultado mensurável (smoke test successful, X anomalies detectadas em janela Y)
  - Esta seção do roadmap conta como justificativa para version bump quando milestone for validado em prod (operador real usar pra tunar regra) — **não bumpar agora**
  - **Depends**: T14

## Não fazer nesta iteração (V2)

- Comparação com ground truth (TPs/FPs/FNs vs AM history)
- ML wired no replay (Isolation Forest stateful em modo replay)
- Replay distribuído (chunks paralelos)
- CI integration (rodar em PR)

## Métricas de sucesso da spec

A spec é considerada bem-sucedida se:

1. Operador consegue rodar `controller --replay` sobre 24h de dados reais e produzir um relatório
2. O relatório identifica corretamente os top 5 workloads ruidosos (correlaciona com observação humana)
3. Mudança de threshold no config + replay produz contagem coerente (mais alto threshold → menos anomalies)
4. Replay roda em ≤ 5min para janela de 24h
5. Zero efeitos colaterais (Redis MONITOR confirma sem escritas; AM logs sem chamadas)

## Estimativa

| Phase | Tasks | Estimativa |
|-------|-------|------------|
| 1 — Foundation | T1, T2, T3 | 1 dia |
| 2 — Baseline store | T4, T5 | 1 dia |
| 3 — Engine | T6, T7, T8 | 2 dias |
| 4 — Output | T9, T10 | 1 dia |
| 5 — CLI | T11, T12 | 0.5 dia |
| 6 — Tests | T13, T14 | 1.5 dias |
| 7 — Docs | T15, T16 | 0.5 dia |
| **Total** | | **~7.5 dias** |

Sequencial. Paralelização teórica (se subagent funcionasse): T2/T3 paralelos com T4 → ganha ~1 dia.
