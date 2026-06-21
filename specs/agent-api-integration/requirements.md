# Feature: Agent API Integration

## Visão geral

O controller de anomaly-detection invoca a Agent API do staffops-chaitops quando detecta anomalias com alta confiança, disparando uma investigação automatizada por squad de agentes. O objetivo é que os membros do squad iniciem a investigação com ~80% do diagnóstico já pronto (enrichment, ML score, correlações, links de observabilidade).

## User Stories

### US-1: Disparo automático para Agent API

WHEN uma anomalia é detectada com severity >= warning AND (ml_score >= 0.7 OR correlation_group_size >= 3)
THEN the system SHALL enviar o payload enriquecido para a Agent API via HTTP POST
AND the system SHALL NOT bloquear o fluxo normal de alertas enquanto aguarda resposta.

### US-2: Payload enriquecido com contexto completo

WHEN o controller dispara uma chamada para a Agent API
THEN the system SHALL incluir no payload: tipo de detector, serviço afetado, enrichment bundle (cpu_ratio, memory_ratio, error_rate, latency_p99, restarts), ML score com contributors, correlation group, e links (Grafana, Tempo, Loki).

### US-3: Fallback para fluxo normal

WHEN a Agent API estiver indisponível ou retornar erro
THEN the system SHALL continuar o fluxo normal de alerta via Alertmanager
AND the system SHALL registrar o erro em log (warn level)
AND the system SHALL incrementar métrica de falha.

### US-4: Circuit breaker

WHEN a Agent API falhar 3 vezes em 5 minutos
THEN the system SHALL desabilitar chamadas à Agent API por 10 minutos
AND the system SHALL registrar a abertura do circuit breaker em log (error level)
AND the system SHALL emitir métrica de circuit breaker open.

### US-5: Configuração dinâmica

WHEN o operador configurar `agent_api.enabled = false`
THEN the system SHALL não fazer nenhuma chamada à Agent API
AND the system SHALL não avaliar trigger conditions.

### US-6: Throttling e deduplicação

WHEN múltiplas anomalias disparam em janela curta (burst)
THEN the system SHALL limitar chamadas à Agent API a no máximo `max_concurrent` (default: 5) goroutines simultâneas
AND the system SHALL descartar chamadas excedentes com log + métrica (não enfileirar indefinidamente)
AND the system SHALL deduplicar: apenas 1 chamada por `correlation.group_id` dentro de `dedup_window` (default: 5min).

### US-7: Invocação por workload-level alert (não por pod)

WHEN o correlator emitir um workload-level alert (P2.4 pattern — ≥3 sibling pods)
THEN the system SHALL invocar Agent API UMA VEZ para o alerta workload-level
AND the system SHALL NOT invocar separadamente para cada pod-level alert suprimido.

### US-8: Graceful shutdown

WHEN o controller receber SIGTERM
THEN the system SHALL aguardar goroutines in-flight (até `terminationGracePeriodSeconds`)
AND the system SHALL NOT iniciar novas chamadas à Agent API após SIGTERM.

## Acceptance Criteria

- [ ] Anomalias que atendem trigger conditions (severity >= warning AND (ml_score >= 0.7 OR correlation_group_size >= 3)) disparam chamada HTTP POST à Agent API
- [ ] Anomalias que NÃO atendem trigger conditions seguem apenas o fluxo Alertmanager
- [ ] Payload enviado contém todos os campos: source, alert, enrichment, ml, correlation, links
- [ ] Chamada é fire-and-forget: controller não espera resultado do squad
- [ ] Timeout configurável (default: 5s) — se exceder, trata como falha
- [ ] Falha na Agent API não impede envio do alerta ao Alertmanager
- [ ] Circuit breaker abre após 3 falhas em 5min, fecha após 10min
- [ ] Circuit breaker half-open: após recovery_timeout, permite 1 request de prova antes de fechar
- [ ] Métricas expostas: `agent_api_requests_total{status}`, `agent_api_duration_seconds`, `agent_api_circuit_breaker_state`, `agent_api_circuit_breaker_transitions_total{from,to}`
- [ ] Feature desabilitável via config (`agent_api.enabled: false`) com hot-reload (sem restart)
- [ ] Testes unitários com ≥90% de cobertura no package `internal/agentapi/`
- [ ] Funciona APENAS quando controller NÃO está em dry-run mode
- [ ] Max `max_concurrent` goroutines simultâneas para chamadas Agent API (default: 5)
- [ ] Deduplicação por `correlation.group_id` dentro de `dedup_window` (default: 5min)
- [ ] Workload-level alerts invocam Agent API uma vez (não N vezes por pod)
- [ ] Graceful shutdown: goroutines in-flight completam antes de exit
- [ ] Log decay: após circuit breaker abrir, logs reduzem a 1 por minuto (não 1 por anomalia skipped)
- [ ] Payload máximo: 64KB — truncar `ml.features`/`ml.contributors` se exceder

## Fora de escopo

- Receber callback da Agent API (o resultado vai para Slack via chaitops)
- Implementar retry com backoff exponencial (circuit breaker é suficiente)
- Autenticação mTLS com a Agent API (fase futura — v1 usa bearer token simples)
- Modificar o schema de resposta do chaitops
- Sair do dry-run mode (precondição externa, não parte desta feature)
- Dashboard Grafana dedicado para Agent API (será criado separadamente)
- Batching de múltiplas anomalias num único POST (v1 é 1:1, batching se volume justificar)

## Precondições (roadmap alignment)

Esta feature SÓ faz sentido APÓS:
- **P5.3** — Controller fora de dry-run mode (alertas reais)
- **P2.4 prod-validado** — Workload-aware correlation funcional

Posição no roadmap: **nova fase P5.5** (após P5.3 validação de alertas reais). Implementar antes seria dead code — Agent API nunca seria chamada em dry-run.
