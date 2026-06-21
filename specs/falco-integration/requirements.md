# Feature: Falco Integration (runtime security signal)

## Visão geral

O controller de anomaly-detection passa a consumir eventos do **Falco** (runtime
security via eBPF/syscalls) como uma **nova fonte de sinal**, ao lado das três já
existentes: métricas (VictoriaMetrics), logs (Loki) e eventos do K8s. Hoje o
sistema enxerga *saturação e erro* (CPU, memória, latência, error rate, restarts).
Falco adiciona uma dimensão ortogonal: *comportamento suspeito em runtime* — shell
aberto em container, escrita em path sensível, escalação de privilégio, conexão de
rede inesperada, troca de binário.

O valor central é **correlação cross-signal de segurança**: quando um pod já está
anômalo em recurso/latência **E** o Falco disparou uma regra de alta prioridade no
mesmo pod, no mesmo período, o alerta muda de natureza — deixa de ser "pod sob
carga" e passa a ser "possível comprometimento". Isso eleva severidade e enriquece
o contexto que o operador (ou o squad via P5.5) recebe.

Esta feature **não** substitui o alerting nativo do Falco/Falcosidekick. O escopo
v1 é **enriquecer e correlacionar** anomalias existentes com sinal de segurança,
não reimplementar a detecção do Falco.

## User Stories

### US-1: Falco como nova fonte de sinal

WHEN o controller está configurado com `falco.enabled = true`
THEN o sistema SHALL ingerir eventos do Falco e normalizá-los para o tipo interno
de anomalia com `Signal = "security"` e `Detector = "falco"`
AND o sistema SHALL extrair identidade (namespace, pod, workload) de cada evento
reusando `correlation.ExtractWorkload`.

### US-2: Correlação com anomalias existentes

WHEN um evento Falco para o pod P chega dentro da janela de correlação de uma
anomalia de recurso/latência existente para o mesmo pod P
THEN o sistema SHALL agrupar os dois sinais no mesmo `CorrelatedAlert`
AND o sistema SHALL adicionar `"security"` à lista `Signals` do alerta.

### US-3: Escalada de severidade em correlação de segurança

WHEN uma anomalia existente correlaciona com um evento Falco de prioridade
`Critical`/`Alert`/`Emergency`
THEN o sistema SHALL escalar a severidade do alerta para `critical`
AND o sistema SHALL registrar o motivo da escalada nas anotações (`escalation_reason = "falco_correlation"`).

### US-4: Enriquecimento do alerta com contexto Falco

WHEN um alerta inclui sinal de segurança
THEN o sistema SHALL adicionar anotações: `falco_rule`, `falco_priority`,
`falco_source` (syscall/k8s_audit), e um link de investigação (Loki LogQL para os
logs do Falco do pod no período).

### US-5: Controle de cardinalidade

WHEN o sistema emite métricas sobre eventos Falco
THEN o sistema SHALL usar APENAS labels de baixa cardinalidade bounded
(`priority`, `namespace`, `source`)
AND o sistema SHALL NUNCA usar `rule`, `output`, `pod`, ou `container_id` como
label de métrica (vão para anotações/logs).

### US-6: Configuração e desabilitação

WHEN o operador configura `falco.enabled = false`
THEN o sistema SHALL não ingerir nem processar nenhum evento Falco
AND a mudança SHALL ser aplicável via hot-reload (config.Watcher), sem restart.

### US-7: Degradação graciosa da fonte Falco

WHEN a fonte de eventos Falco está indisponível (endpoint/stream down, Loki sem
logs do Falco)
THEN o sistema SHALL continuar operando com as três fontes existentes
(metrics/logs/events) sem bloquear nem crashar
AND o sistema SHALL registrar o erro (warn, rate-limited) e incrementar métrica de falha.

### US-8: Filtro de prioridade mínima

WHEN o operador configura `falco.min_priority = "Warning"`
THEN o sistema SHALL descartar eventos Falco abaixo dessa prioridade antes de
qualquer processamento
AND o filtro SHALL ser aplicado o mais cedo possível na ingestão (evita custo de
correlação para ruído de baixa prioridade).

### US-9: Falco isolado NÃO dispara alerta sozinho (v1)

WHEN um evento Falco chega mas NÃO há anomalia de recurso/latência correlacionada
para o mesmo pod na janela
THEN o sistema SHALL NÃO emitir um alerta novo (evita duplicar o alerting nativo
do Falco)
AND o sistema SHALL registrar o evento (debug/metric) para observabilidade
AND este comportamento SHALL ser revisitado em v2 (ver Fora de escopo).

## Acceptance Criteria

- [ ] Eventos Falco são normalizados para anomalia interna com `Signal="security"`, `Detector="falco"`
- [ ] Identidade (namespace/pod/workload) extraída via `correlation.ExtractWorkload`
- [ ] Eventos Falco correlacionam com anomalias existentes dentro da janela de correlação por pod
- [ ] Correlação de segurança com prioridade ≥ Critical escala severidade para `critical`
- [ ] Alertas com sinal de segurança carregam anotações `falco_rule`, `falco_priority`, `falco_source` + link Loki
- [ ] Métricas usam apenas labels bounded (`priority`, `namespace`, `source`) — zero labels de alta cardinalidade
- [ ] `falco.enabled = false` desativa toda a feature; hot-reloadable sem restart
- [ ] Falco source indisponível NÃO bloqueia nem crasha o pipeline existente
- [ ] `falco.min_priority` filtra eventos cedo na ingestão
- [ ] Evento Falco sem anomalia correlacionada NÃO emite alerta novo (v1)
- [ ] Métricas expostas: `staffops_ad_falco_events_total{priority,namespace,source}`, `staffops_ad_falco_events_dropped_total{reason}`, `staffops_ad_falco_correlations_total{result}`, `staffops_ad_falco_ingestion_errors_total`
- [ ] Funciona em ambos os modos de ingestão suportados (decisão A vs B — ver design)
- [ ] Testes unitários com ≥90% de cobertura no package `internal/falco/`
- [ ] Cardinalidade total das séries `staffops_ad_falco_*` documentada e dentro do limite de steering (≤2000/métrica)

## Fora de escopo (v1)

- **Falco disparando alerta isolado** (sem anomalia correlacionada). v1 só enriquece;
  emitir alerta de segurança standalone duplicaria o Falcosidekick. Reabrir em v2 se
  houver demanda de "security-only alerts" passando pelo mesmo pipeline de
  enriquecimento/links.
- **Deploy do Falco + Falcosidekick** — é pré-requisito de infra, responsabilidade de
  `gitops`/`security`, não desta feature.
- **Regras customizadas do Falco** (`falco_rules.local.yaml`) — fora; consumimos o que
  o Falco já emite.
- **Resposta automática** (kill pod, quarentena, NetworkPolicy) — fora; o controller
  apenas observa e sinaliza.
- **k8s_audit como fonte primária** — v1 foca em eventos de syscall; k8s_audit pode ser
  ingerido se vier no mesmo stream, mas não é alvo de design dedicado.
- **Feedback loop de FP/FN para regras Falco** — fora (relacionado a P3.3).
- **Persistência histórica de eventos Falco** — fora; eventos são efêmeros, correlação
  é in-window.

## Decisões em aberto (resolver no design antes de implementar)

1. **Falco já está deployado nos clusters alvo?** Se não, P2.7 fica bloqueado em
   pré-req de infra (delegar a `gitops`/`security`). Confirmar versão do Falco e se
   Falcosidekick está presente.
2. **Modo de ingestão: pull (Loki) vs push (webhook/gRPC)?** Ver design — duas opções
   com trade-offs. Decisão afeta acoplamento, latência e reuso de pipeline.
3. **Janela de correlação de segurança = janela de correlação atual, ou janela
   própria?** Eventos de segurança podem preceder a saturação (ex: cryptominer:
   shell → CPU spike minutos depois). Avaliar janela assimétrica/maior para `security`.
4. **Mapeamento prioridade Falco → severidade do alerta.** Falco usa
   Emergency/Alert/Critical/Error/Warning/Notice/Informational/Debug. Definir o corte
   que escala para `critical` vs `warning`.

## Precondições (roadmap alignment)

- **Pré-req de infra**: Falco + Falcosidekick deployados e acessíveis no cluster alvo
  (validar com `gitops`/`security`).
- **Decisão de ingestão (A vs B)** fechada no design antes de codar.
- Posição no roadmap: **P2.7** (Phase 2 — ML maturity / correlação). Independente de
  dry-run (enriquecimento funciona mesmo em dry-run, diferente da P5.5).
