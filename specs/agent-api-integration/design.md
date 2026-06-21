# Design: Agent API Integration

## Arquitetura

```
┌─────────────────────────────────────────────────────────────┐
│                   anomaly-detection-controller               │
│                                                             │
│  ┌──────────┐    ┌──────────────┐    ┌─────────────────┐   │
│  │ Detector │───▶│ Enrichment   │───▶│ Alert Pipeline  │   │
│  │ (existing)│   │ (existing)   │    │                 │   │
│  └──────────┘    └──────────────┘    │  ┌───────────┐  │   │
│                                      │  │ Trigger   │  │   │
│                                      │  │ Evaluator │  │   │
│                                      │  └─────┬─────┘  │   │
│                                      │        │        │   │
│                                      │   ┌────▼────┐   │   │
│                                      │   │ Agent   │   │   │
│                                      │   │ API     │   │   │
│                                      │   │ Client  │   │   │
│                                      │   └────┬────┘   │   │
│                                      │        │        │   │
│                                      │  ┌─────▼─────┐  │   │
│                                      │  │Alertmanager│  │   │
│                                      │  │ (existing) │  │   │
│                                      │  └───────────┘  │   │
│                                      └─────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            │
                    HTTP POST (fire-and-forget)
                            │
                            ▼
               ┌─────────────────────────┐
               │  staffops-chaitops      │
               │  Agent API              │
               │  POST /webhooks/anomaly │
               └────────────┬────────────┘
                            │
                            ▼
                    Squad Investigation
                    (resultado → Slack)
```

## Componentes

| Componente | Responsabilidade | Localização |
|-----------|-----------------|-------------|
| `TriggerEvaluator` | Avalia se anomalia atende condições para disparo | `internal/agentapi/trigger.go` |
| `Client` | HTTP client com timeout, circuit breaker, métricas, bounded concurrency | `internal/agentapi/client.go` |
| `CircuitBreaker` | Controle de falhas — abre após 3 falhas em 5min, half-open probe, fecha após sucesso | `internal/agentapi/circuitbreaker.go` |
| `PayloadBuilder` | Monta o JSON payload a partir do alert enriched (com size cap 64KB) | `internal/agentapi/payload.go` |
| `Config` | Struct de configuração (`agent_api.*`), participates de hot-reload via config.Watcher | `internal/agentapi/config.go` |
| `Deduplicator` | Redis-backed dedup por `correlation.group_id` com TTL = `dedup_window` | `internal/agentapi/dedup.go` |

## Fluxo de execução

```
1. Alert pipeline recebe anomalia enriquecida (existente)
2. SE dry_run == true → skip (não avalia trigger)
3. SE agent_api.enabled == false → skip
4. SE alert é pod-level E pertence a workload pattern suprimido → skip
   (Agent API é chamada apenas para o workload-level alert emitido pelo correlator)
5. TriggerEvaluator avalia condições:
   - severity >= warning?
   - ml_score >= 0.7 OR correlation_group_size >= 3?
6. SE trigger == false → skip (só Alertmanager)
7. Deduplicator.Check(correlation.group_id):
   - SE já chamou para este group_id nos últimos dedup_window → skip + metric
8. PayloadBuilder monta JSON (cap 64KB — trunca features/contributors se necessário)
9. Client.Send() — bounded fire-and-forget:
   - Semaphore (buffered channel, cap=max_concurrent): tenta adquirir slot
   - SE semaphore full → discard + log(warn, rate-limited) + metric(status=throttled)
   - SE shutting_down → discard
   - Goroutine executa:
     a. Circuit breaker open? → skip + log(1/min decay) + metric(status=circuit_open)
     b. HTTP POST com timeout
     c. Sucesso → metric(status=success), circuit breaker reset count
     d. Falha → metric(status=failure), circuit breaker increment
     e. Release semaphore slot
10. Alert pipeline continua para Alertmanager (independente do resultado acima — step 10 é paralelo a step 9)
```

### Shutdown sequence

```
1. Controller recebe SIGTERM
2. agent_api.Client.Shutdown() chamado
3. shutting_down = true (novas chamadas rejeitadas)
4. Wait group aguarda goroutines in-flight (max 5s ou terminationGracePeriodSeconds)
5. Goroutines em timeout são canceladas via context
```

## Payload schema

```json
{
  "source": "anomaly-detection",
  "alert": {
    "alertname": "string",
    "severity": "string",
    "service": "string",
    "namespace": "string",
    "cluster": "string",
    "detector": "string",
    "value": 0.0,
    "threshold": 0.0
  },
  "enrichment": {
    "cpu_ratio": 0.0,
    "memory_ratio": 0.0,
    "error_rate_1m": 0.0,
    "latency_p99_5m": 0,
    "restarts_5m": 0
  },
  "ml": {
    "score": 0.0,
    "features": ["string"],
    "contributors": ["string"]
  },
  "correlation": {
    "group_id": "string",
    "group_size": 0,
    "workload": "string",
    "pattern": "string"
  },
  "links": {
    "grafana": "string",
    "tempo": "string",
    "loki": "string"
  }
}
```

## Configuração

```yaml
agent_api:
  enabled: true  # hot-reloadable via config.Watcher (no restart needed)
  url: "https://chaitops.bdc.app.br/webhooks/anomaly"
  timeout: 5s
  auth_token_file: "/etc/secrets/agent-api-token"
  max_payload_bytes: 65536  # 64KB — truncate features/contributors if exceeded
  max_concurrent: 5         # bounded goroutine pool (semaphore)
  dedup_window: 5m          # suppress duplicate calls for same correlation.group_id
  trigger_conditions:
    min_severity: "warning"
    min_ml_score: 0.7
    min_correlation_group_size: 3
  circuit_breaker:
    failure_threshold: 3
    failure_window: 5m
    recovery_timeout: 10m
    # After recovery_timeout, circuit enters half-open: allows 1 probe request.
    # Success → closed. Failure → re-open for another recovery_timeout.
```

## Métricas expostas

| Métrica | Tipo | Labels | Descrição |
|---------|------|--------|-----------|
| `agent_api_requests_total` | Counter | `status={success,failure,circuit_open,throttled,deduped,skipped}` | Total de chamadas (ou não-chamadas) por motivo |
| `agent_api_duration_seconds` | Histogram | — | Latência das chamadas HTTP (buckets: 0.1, 0.25, 0.5, 1, 2.5, 5) |
| `agent_api_circuit_breaker_state` | Gauge | — | 0=closed, 1=open, 2=half-open |
| `agent_api_circuit_breaker_transitions_total` | Counter | `from={closed,open,half_open}`, `to={closed,open,half_open}` | Transições de estado — operador vê quando abre/fecha |
| `agent_api_inflight_goroutines` | Gauge | — | Goroutines ativas (0 a max_concurrent) |
| `agent_api_payload_bytes` | Histogram | — | Tamanho do payload enviado |

## Rationale

### Decisão 1: Fire-and-forget (goroutine sem esperar resultado)

**Escolha**: O controller dispara a chamada HTTP em goroutine separada e não aguarda resposta do squad.

**Justificativa, em ordem de força**:
1. O alert pipeline não pode ter latência adicional — o Alertmanager precisa receber o alerta sem atraso, pois é o canal primário de notificação
2. O resultado do squad vai para Slack (canal assíncrono por natureza) — não há o que fazer com a resposta no controller
3. Simplifica error handling — falha na Agent API é um evento de log/métrica, não precisa propagar

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Sem confirmação de que o squad iniciou investigação | Métrica `agent_api_requests_total{status=success}` é proxy suficiente; se chaitops recebeu, ele processa |
| Possível perda de payload se goroutine morrer (crash) | Aceitável — o alerta ainda vai pro Alertmanager; Agent API é bonus, não SLA |

**Quando essa decisão estaria errada**:
- Se surgir necessidade de rastrear "o squad respondeu pra esse alerta específico?" (correlation request→response)
- Se o volume de goroutines paralelas causar pressão de memória (improvável com circuit breaker limitando)

### Decisão 2: Circuit breaker local (não biblioteca externa)

**Escolha**: Implementar circuit breaker com half-open probe (counter + timer + state machine) em vez de usar lib como `sony/gobreaker`.

**Justificativa, em ordem de força**:
1. A lógica é simples (3 states, 3 falhas em 5min → open por 10min → half-open probe) — não justifica dependência externa
2. Menos surface area de ataque (supply chain)
3. Testável com time injection (clock interface)

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Reimplementação de pattern comum | ~80 linhas de código com half-open, menos que o boilerplate de integrar lib |
| Sem exponential backoff no recovery | Aceitável — timeout fixo + single probe é previsível |

**Quando essa decisão estaria errada**:
- Se a Agent API tiver padrões de falha intermitente complexos (partial failures, rate-limiting com Retry-After headers)
- Se outros clientes HTTP no controller também precisarem de circuit breaker (nesse caso, extrair lib interna)

### Decisão 3: Bearer token via file (não env var)

**Escolha**: Ler token de autenticação de arquivo montado (`auth_token_file`) em vez de env var.

**Justificativa, em ordem de força**:
1. Segue padrão BDC de secrets via volume mount (12-factor steering: envFile for secrets)
2. Permite rotação sem restart do pod (kubelet atualiza volume)
3. Não aparece em `kubectl describe pod`

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Complexidade extra de montar volume no Helm chart | Padrão já usado por outros secrets no controller |
| Hot-reload do token exige re-leitura periódica | Ler a cada request é negligível em I/O (file cache do kernel) |

**Quando essa decisão estaria errada**:
- Se a Agent API migrar para mTLS (token file vira irrelevante, substitui por cert mount)

## Invariantes

- Agent API NUNCA bloqueia o fluxo para Alertmanager — são caminhos paralelos independentes
- Agent API NUNCA é chamada em dry-run mode
- Agent API NUNCA é chamada se `enabled == false`
- Circuit breaker open NUNCA causa panic ou erro propagado — apenas skip + log + metric
- Payload SEMPRE contém todos os campos obrigatórios (campos ausentes → field zero value, não omissão)
- Payload NUNCA excede `max_payload_bytes` (truncar arrays antes de serializar)
- Token NUNCA aparece em logs ou métricas
- Goroutines NUNCA excedem `max_concurrent` (semaphore é hard cap, não soft limit)
- Goroutines in-flight SEMPRE completam ou timeout em shutdown (sem leak)
- Log de "circuit open, skipping" NUNCA excede 1 mensagem/minuto (decay via sampled logger)
- Enrichment latency (Redis cache miss) NUNCA atrasa Agent API call — payload usa dados já disponíveis no alert pipeline (enrichment é upstream, já executado)

## Dependências externas

| Serviço | Propósito | Criticidade |
|---------|-----------|-------------|
| staffops-chaitops Agent API | Receptor do payload, dispara squad | Soft dependency (fallback: Alertmanager) |
| Alertmanager | Canal primário de alertas | Hard dependency (existente) |
| Redis | Cache de enrichment (existente) | Hard dependency (existente) |

## SRE Review Notes

**Reviewer**: SRE specialist | **Date**: 2026-06-02

### Findings & Changes Applied

| # | Issue | Severity | Resolution |
|---|-------|----------|------------|
| 1 | Unbounded goroutines — original "fire-and-forget goroutine" had no concurrency limit. 20 anomalies/min = 20 goroutines = 60+ kiro-cli processes downstream. | HIGH | Added `max_concurrent` semaphore (default 5) + `status=throttled` metric. Excess discarded, not queued. |
| 2 | No deduplication — same correlation group could trigger multiple squads. | HIGH | Added `Deduplicator` component with Redis TTL = `dedup_window`. Same group_id suppressed for 5min. |
| 3 | Workload vs pod invocation ambiguity — spec didn't specify whether Agent API fires per-pod or per-workload. | HIGH | Added US-7: Agent API fires ONCE for workload-level alert. Pod-level alerts suppressed by P2.4 do NOT trigger Agent API. |
| 4 | No half-open state in circuit breaker — fixed recovery timeout means blind 10min wait with no validation that API recovered. | MEDIUM | Added half-open state: after recovery_timeout, 1 probe request. Success → close. Failure → re-open. |
| 5 | Operator visibility of circuit breaker — original only had state gauge. No way to see transitions over time. | MEDIUM | Added `circuit_breaker_transitions_total{from,to}` counter. Operators can alert on `rate(transitions{to="open"}[5m]) > 0`. |
| 6 | Log spam when API down for days — every skipped anomaly would log at warn. | MEDIUM | Added log decay invariant: max 1 msg/min when circuit open (sampled logger). |
| 7 | Hot-reload not confirmed — spec said `enabled` is configurable but didn't confirm it hot-reloads. | LOW | Confirmed controller has `config.Watcher`. Config comment now explicitly states `agent_api.enabled` is hot-reloadable. |
| 8 | Graceful shutdown missing — goroutines could leak on SIGTERM. | MEDIUM | Added US-8 + shutdown sequence in flow. WaitGroup + context cancellation. |
| 9 | Payload size unbounded — ML features/contributors lists could grow. | LOW | Added `max_payload_bytes: 64KB` with truncation logic. |
| 10 | Enrichment latency concern — Redis cache miss could delay API call. | LOW | Clarified in invariants: enrichment is upstream (already in alert pipeline). PayloadBuilder reads already-enriched data. No extra Redis call. |
| 11 | Dry-run mode testing — how to validate integration without real Agent API. | LOW | Addressed in tasks (httptest integration test). Also: when `dry_run=true`, entire agent_api path is skipped — testable by running with `dry_run=false` + httptest mock URL in config. |
| 12 | Roadmap misalignment — feature requires controller out of dry-run (P5.3). | HIGH | Added "Precondições" section in requirements.md. Feature is P5.5, not implementable until P5.3 validated. |

### Recommendations (não implementadas nesta review — decision points para o usuário)

1. **Considerar rate-limit no chaitops lado** — mesmo com `max_concurrent=5` no controller, um cluster instável pode gerar 5 squads a cada 5min. O chaitops deveria ter seu próprio throttle (aceitar ou 429).

2. **Alerting rule sugerida** — criar VMRule quando implementar:
   ```yaml
   - alert: AgentAPICircuitBreakerOpen
     expr: agent_api_circuit_breaker_state == 1
     for: 5m
     labels:
       severity: warning
     annotations:
       summary: "Agent API circuit breaker open for >5min"
       runbook_url: "..."
   ```

3. **Error budget impact** — se Agent API causar goroutine leak (bug), isso pode degradar detection cycle latency. Monitorar `cycle_duration_seconds` com threshold tighter quando agent_api está habilitado. Sugerir: `agent_api_inflight_goroutines > max_concurrent * 0.8 for 5m → warning`.

4. **Blast radius de Agent API timeout** — o timeout de 5s é per-request, mas com 5 goroutines simultaneamente tendo timeout, o impacto total em memory/fd é ~5 TCP connections stuck. Aceitável. Não muda se timeout subir para 10s. Não alterar.

5. **Revisão da janela do circuit breaker** — 3 falhas em 5min é adequado para um sistema que chama Agent API ~5-20 vezes por detection cycle. Se volume cair para <1/min (cluster saudável), 3 falhas consecutivas podem fechar o circuit legitimamente. Se volume subir para >50/min, 3 falhas pode ser noise. Considerar threshold como % do volume (`failure_rate > 50% in window` em vez de absolute count) em v2.

6. **Observability do dedup** — `status=deduped` na métrica é suficiente para v1. Em v2, considerar expor `agent_api_dedup_cache_size` gauge para visibilidade do Redis usage.
