# Tasks: Agent API Integration

## Phase 1: Core package

- [ ] Task 1: Criar package `internal/agentapi/` com struct `Config` e parsing YAML (incluir novos campos: max_concurrent, dedup_window, max_payload_bytes)
- [ ] Task 2: Implementar `PayloadBuilder` — converte alert enriched para JSON schema definido, com truncation se payload > max_payload_bytes
- [ ] Task 3: Implementar `TriggerEvaluator` — avalia severity + ml_score + correlation_group_size + rejeita pod-level alerts suprimidos por workload pattern
- [ ] Task 4: Implementar `CircuitBreaker` — 3 states (closed/open/half-open), counter com window temporal, clock interface para testes
- [ ] Task 5: Implementar `Client` — HTTP POST com timeout, bearer token de file, circuit breaker, semaphore (max_concurrent), WaitGroup para shutdown (depends on: Task 2, Task 4)
- [ ] Task 6: Implementar `Deduplicator` — Redis SET com TTL=dedup_window, key=`agentapi:dedup:{group_id}`

## Phase 2: Integração no controller

- [ ] Task 7: Adicionar config `agent_api.*` ao `config.yaml` e struct de configuração do controller; confirmar que config.Watcher propaga mudanças em `agent_api.enabled` (depends on: Task 1)
- [ ] Task 8: Integrar no alert pipeline — chamar `TriggerEvaluator` + `Deduplicator.Check()` + `Client.Send()` após enrichment, antes de Alertmanager (pipeline paralela) (depends on: Task 3, Task 5, Task 6, Task 7)
- [ ] Task 9: Garantir que dry-run mode skip completo (não avalia trigger, não chama API) (depends on: Task 8)
- [ ] Task 10: Implementar shutdown hook — `Client.Shutdown()` chamado no SIGTERM handler, WaitGroup drain com timeout (depends on: Task 5)

## Phase 3: Observabilidade

- [ ] Task 11: Registrar métricas Prometheus: `agent_api_requests_total{status}`, `agent_api_duration_seconds`, `agent_api_circuit_breaker_state`, `agent_api_circuit_breaker_transitions_total{from,to}`, `agent_api_inflight_goroutines`, `agent_api_payload_bytes` (depends on: Task 5)
- [ ] Task 12: Adicionar logs estruturados com sampled logger: trigger result, send success/failure, circuit breaker transitions (1/event), circuit open skip (1/min max) (depends on: Task 8)

## Phase 4: Testes

- [ ] Task 13: Testes unitários para `TriggerEvaluator` — tabela com combinações de severity/ml_score/group_size + workload suppression (depends on: Task 3)
- [ ] Task 14: Testes unitários para `CircuitBreaker` — clock mockado, validar closed→open→half-open→closed transitions (depends on: Task 4)
- [ ] Task 15: Testes unitários para `Client` — httptest server, validar payload/headers/timeout/circuit breaker/semaphore full/shutdown (depends on: Task 5)
- [ ] Task 16: Testes unitários para `PayloadBuilder` — validar schema JSON + truncation em payload grande (depends on: Task 2)
- [ ] Task 17: Testes unitários para `Deduplicator` — miniredis, validar dedup + TTL expiry (depends on: Task 6)
- [ ] Task 18: Teste de integração — alert pipeline end-to-end com httptest simulando Agent API: burst de 10 anomalias → verifica max 5 concurrent, dedup funciona, circuit breaker abre (depends on: Task 8)
- [ ] Task 19: Validar cobertura ≥90% no package `internal/agentapi/` (depends on: Task 13-18)

## Phase 5: Documentação

- [ ] Task 20: Atualizar README do controller com seção sobre Agent API integration (depends on: Task 8)
- [ ] Task 21: Documentar config no `config.yaml.example` (depends on: Task 7)
- [ ] Task 22: Adicionar item P5.5 no ROADMAP.md referenciando esta spec (depends on: Task 20)
