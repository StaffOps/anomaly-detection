# Tasks: Falco Integration (runtime security signal)

> **Status**: `BLOCKED` — Waiting on Phase 0 prereqs (Falco deployed? Ingest mode decision)

> Verification independence (steering): autor do código ≠ autor dos testes. Onde
> houver subagents disponíveis, delegar implementação a `dev` e testes a uma sessão
> `dev` distinta, com review por `code-review`. Gate de cobertura ≥90% no package
> `internal/falco/`.

## Phase 0: Pré-requisitos (bloqueiam o resto)

- [ ] Task 0.1: Confirmar com `gitops`/`security` que Falco + Falcosidekick estão deployados no cluster alvo, versão, e se o output Loki está habilitado (resolve Decisão em aberto #1)
- [ ] Task 0.2: Fechar a decisão de ingestão A (Loki pull) vs B (webhook) como default — design propõe A; validar com `security` (superfície de rede) e `sre` (latência aceitável) (resolve Decisão em aberto #2)
- [ ] Task 0.3: Definir mapeamento prioridade Falco → severidade e janela de correlação de segurança (resolve Decisões em aberto #3 e #4); registrar no design

## Phase 1: Core package `internal/falco/`

- [ ] Task 1: Criar `Event` struct + `Config` struct com parsing YAML (`falco.*`) (depends on: Task 0.3)
- [ ] Task 2: Implementar `Normalizer` — `Event` → `detection.Anomaly{Signal:"security", Detector:"falco"}`, extração de identidade via `correlation.ExtractWorkload`, filtro `min_priority`, mapeamento prioridade→severidade (depends on: Task 1)
- [ ] Task 3: Definir interface `Source` (emite `Event` em canal; método `Start(ctx)`/`Close`) (depends on: Task 1)
- [ ] Task 4: Implementar `LokiSource` (modo A) — poll LogQL via `LogsPoller` existente, parse JSON do Falco, emite eventos; erro de poll → metric + log rate-limited (depends on: Task 3)
- [ ] Task 5: Implementar `WebhookSource` (modo B) — HTTP receiver com bearer token de file, validação/limite de body, fail-closed sem token; emite eventos (depends on: Task 3)

## Phase 2: Integração no Correlator + controller

- [ ] Task 6: Estender `Correlator` para aceitar sinal `security`: merge de evento Falco com anomalia non-security do mesmo pod na janela, adiciona `"security"` a `Signals` (depends on: Task 2)
- [ ] Task 7: Implementar escalada de severidade por correlação Falco (prioridade ≥ `critical_priority` → `critical`, annotation `escalation_reason`) (depends on: Task 6)
- [ ] Task 8: Implementar supressão v1 — sinal `security` isolado (sem correlação) NÃO emite alerta; metric `security_only_suppressed` (depends on: Task 6)
- [ ] Task 9: Wire da `Source` no boot do controller (selecionada por `falco.mode`); só ativa quando `falco.enabled`; canal alimenta o mesmo pipeline das outras fontes (depends on: Task 4, Task 5, Task 6)
- [ ] Task 10: Confirmar hot-reload de `falco.enabled` via `config.Watcher`; desabilitar para a source quando false (depends on: Task 9)
- [ ] Task 11: Graceful degradation — falha/ausência da Falco source não bloqueia nem derruba o pipeline existente; graceful shutdown da source no SIGTERM (depends on: Task 9)

## Phase 3: Enriquecimento e observabilidade

- [ ] Task 12: Adicionar anotações `falco_rule`, `falco_priority`, `falco_source` no alerta correlacionado; estender `LinkBuilder` com link Loki para o evento Falco (modo A) (depends on: Task 7)
- [ ] Task 13: Registrar métricas Prometheus: `staffops_ad_falco_events_total{priority,namespace,source}`, `staffops_ad_falco_events_dropped_total{reason}`, `staffops_ad_falco_correlations_total{result}`, `staffops_ad_falco_ingestion_errors_total{source}` (depends on: Task 9)
- [ ] Task 14: Logs estruturados (sampled): evento ingerido (debug), correlação/escalada (info), erro de ingestão (warn, rate-limited 1/min) (depends on: Task 9)

## Phase 4: Testes (autor distinto do código — verification-independence)

- [ ] Task 15: Testes unitários do `Normalizer` — tabela de prioridades, identidade ausente, filtro min_priority, mapeamento severidade (depends on: Task 2)
- [ ] Task 16: Testes do `LokiSource` — httptest simulando resposta LogQL do Falco, parse, erro de poll (depends on: Task 4)
- [ ] Task 17: Testes do `WebhookSource` — httptest: payload válido/ malformado, sem token (fail-closed), body grande (depends on: Task 5)
- [ ] Task 18: Testes da extensão do `Correlator` — merge security+resource, escalada, supressão security-only, janela assimétrica (depends on: Task 6, Task 7, Task 8)
- [ ] Task 19: Teste de degradação — source down não impacta as 3 fontes existentes (depends on: Task 11)
- [ ] Task 20: Validar cobertura ≥90% no package `internal/falco/` (depends on: Task 15-19)
- [ ] Task 21: Validar cardinalidade real das séries `staffops_ad_falco_*` (≤2000/métrica) via teste ou query local (depends on: Task 13)

## Phase 5: Documentação

- [ ] Task 22: Atualizar README do controller com seção "Falco integration (security signal)" (depends on: Task 9)
- [ ] Task 23: Documentar config `falco.*` no `config.yaml` / exemplo (depends on: Task 1)
- [ ] Task 24: Adicionar página em `docs/detection/` (4ª fonte de sinal) + atualizar `docs/architecture/data-flow.md` (depends on: Task 22)
- [ ] Task 25: Confirmar item P2.7 no ROADMAP.md referencia esta spec (feito na criação da spec; revalidar) (depends on: Task 22)

## Phase 6: Review (gate antes de "done")

- [ ] Task 26: Review do design + diff por `sre` (janela, blast radius, impacto em `cycle_duration_seconds`) e `security` (webhook surface, token, dados sensíveis); aplicar findings como "SRE/Security Review Notes" no design.md (depends on: Task 18, Task 19)
