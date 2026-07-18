# Feature: Replay Mode

## Problema

Hoje toda mudança de regra de detecção (novo threshold, novo `static_rule`, novo `adaptive_metric`, ajuste de Z-score) é validada **só em produção** — operador faz o deploy, observa, depois descobre que ficou ruidoso ou silencioso demais. Custo de aprender via prod: alertas falsos no Slack, fadiga do operador, possíveis incidentes mascarados por ruído.

## Objetivo

Permitir que o operador **simule a detecção sobre dados históricos** com uma configuração candidata, **antes** de aplicá-la em produção. Saída: relatório de detecção com volume, distribuição por detector/severity/namespace, top workloads ruidosos.

## User Stories

WHEN um operador quer mudar `baseline.zscore_threshold` de 3.0 → 4.0 THEN o sistema SHALL permitir rodar `controller --replay` sobre últimos 24h com a nova config E produzir relatório mostrando quantos alertas a mais/menos disparariam.

WHEN um operador quer testar um novo `static_rule` THEN o sistema SHALL replicar o ciclo de detecção sobre janela histórica E reportar quantas vezes a regra disparou, em quais workloads, com quais valores.

WHEN um operador rodou um replay THEN o sistema SHALL produzir output **machine-readable** (JSON) E **human-readable** (markdown) na mesma execução.

WHEN o replay roda THEN o sistema NÃO SHALL escrever em Redis, NÃO SHALL chamar Alertmanager, NÃO SHALL chamar workers via gRPC. Tudo in-process, sem efeitos colaterais.

WHEN um operador especifica janela > capacidade dos datasources (ex: 30 dias) THEN o sistema SHALL falhar rápido com mensagem clara, OU oferecer chunking automático (decisão no design).

## Acceptance Criteria

### Funcional

- [ ] CLI: `controller --replay --from=<duration|timestamp> --to=<duration|timestamp> --config=<path> --output=<path>` aceita argumentos
- [ ] `--from=24h` interpretado como "now - 24h"; `--from=2026-05-30T00:00:00Z` interpretado como timestamp absoluto
- [ ] Output default: `./replay-report.json` (sobrescreve a cada execução). Operador pode usar `--output=replay-$(date +%Y%m%d-%H%M%S).json` se quer histórico.
- [ ] Janela máxima default: 7d. Configurável via `--max-range`. Replay falha rápido se `to - from > max-range`.
- [ ] Janela mínima derivada do warm-up: replay falha se `to - from < 2.5h` (warm-up mínimo / 0.2 fraction).
- [ ] Replay reusa o mesmo código de detecção (`internal/detection/`) que o ciclo normal — nenhuma divergência de comportamento
- [ ] Warm-up dinâmico: `max(20% da janela, baseline.warm_up_samples × tick_interval)`. Para `warm_up_samples=60` e `tick=30s` → mínimo 30min.
- [ ] Detector adaptive honra `IsWarmingUp` igual prod: NÃO emite anomaly se `baseline.Result.Count < warm_up_samples` para aquela série (matches behavior de produção).
- [ ] Static rules disparam corretamente em modo replay (queries range em Prometheus)
- [ ] Adaptive Z-score disparam corretamente (baseline temporário em memória, não Redis)
- [ ] Log patterns disparam corretamente (queries range em Loki)
- [ ] P2.4 (workload-pattern) ativo igual produção
- [ ] Replay mode NÃO chama enrichment (P1.1) por padrão — opt-in via `--enrich` (lento, caro)
- [ ] Replay mode NÃO chama ML (P2.1) — V1 explicitamente sem ML (Isolation Forest stateful, polui histórico de prod). Documentado.

### Robustez

- [ ] Erro de query (Prometheus ou Loki indisponível em algum tick): replay **NÃO aborta**. Skip o tick afetado, log warning, incrementa contador `query_errors`. Final report inclui contagem.
- [ ] Resultado vazio (zero anomalies detectadas) é resultado válido — não é erro.
- [ ] `metadata.result_status` no output: `anomalies_detected` | `no_anomalies` | `partial` (último quando houve query errors).

### Output

- [ ] Output JSON estruturado em `<output>` (default: `./replay-report.json`)
- [ ] Output Markdown em `<output>.md` na mesma corrida (sem segundo flag)
- [ ] **Timezone: UTC sempre** em ambos os formatos. Operador converte se precisar — auto-detect é confuso.
- [ ] JSON contém: tempo de execução, janela, config sumarizada, `result_status`, `query_errors` count, total anomalies, por (detector, severity, signal, namespace), top 20 workloads com mais anomalies, distribuição temporal por hora, `execution_metrics` (replay-only metrics embedded).
- [ ] Markdown traz mesmas estatísticas em formato legível para PR comments / docs

### Performance e safety

- [ ] Replay 24h conclui em ≤ 5 min em hardware desenvolvedor típico
- [ ] Replay 7 dias conclui em ≤ 30 min OU sistema rejeita janela com mensagem clara
- [ ] Modo replay loga claramente "REPLAY MODE — no side effects" no início
- [ ] Métricas Prometheus de produção NÃO são incrementadas em replay mode. Métricas de replay vivem em **registry separado**, embedadas no JSON output (V1 sem Prometheus exposure — V2 expõe se demandado).
- [ ] Falha rápida se Prometheus ou Loki indisponível **antes do replay começar** (sanity check). Indisponibilidade durante replay → graceful skip (ver "Robustez").

### Qualidade

- [ ] Unit tests cobrindo: parse de `--from`/`--to`, warm-up split, output format, query error handling, IsWarmingUp filter
- [ ] Integration test usando docker-compose stack contra dataset sintético (idealmente injetar ~100 amostras conhecidas em Prometheus e validar que detector pega)

## Fora de escopo (deixar para versão posterior)

- **Comparação com ground truth** (TPs/FPs/FNs vs AM alert history). Demanda integração com AM `/api/v2/alerts` ou ingestão de CSV labeled. V2.
- **Replay com ML wired** funcionando como prod (Isolation Forest stateful precisa replay sequencial cuidadoso). V2 com ML instance isolada.
- **Cache de queries para iterar config rápido**: salvar Prometheus/Loki responses em disco, próxima run com `--use-cache` reusa sem re-querying. V2.
- **Output em formato Grafana dashboard JSON** (importável). V2 nice-to-have.
- **Replay distribuído** (workers paralelizando chunks da janela). V2 se performance virar gargalo.
- **Replay de eventos K8s** (não temos histórico de eventos retornável). Static + adaptive + log apenas.
- **CI integration** (rodar replay em PR como check). V2 quando os outputs estiverem estáveis.
- **Prometheus exposure de métricas de replay** (`/replay-metrics` endpoint). V2 sob demanda.

## Não-objetivos

- Replay NÃO substitui validação em prod — é triagem prévia, não certificação.
- Replay NÃO é mecanismo de backfill (não popula baselines reais no Redis).
- Replay NÃO modifica config — só lê e reporta.
