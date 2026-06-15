# Design: Falco Integration (runtime security signal)

## Arquitetura

Falco entra como uma **quarta fonte de ingestão**, paralela às três existentes. O
ponto de junção é o `Correlator`, que já agrupa anomalias por workload dentro de uma
janela temporal. Eventos Falco viram `detection.Anomaly` com `Signal="security"` e
fluem pelo mesmo caminho — reusando enrichment, dedup, e dispatch.

```
┌──────────────────────────────────────────────────────────────────────┐
│                     anomaly-detection-controller                       │
│                                                                        │
│  Fontes de ingestão (existentes)            Nova fonte                 │
│  ┌─────────────┐ ┌──────────┐ ┌──────────┐  ┌────────────────────┐    │
│  │ Metrics     │ │ Logs     │ │ K8s      │  │ Falco              │    │
│  │ (VM/PromQL) │ │ (Loki)   │ │ Events   │  │ (Loki LogQL  ──A    │    │
│  │             │ │          │ │ (watch)  │  │  OR webhook  ──B)   │    │
│  └──────┬──────┘ └────┬─────┘ └────┬─────┘  └─────────┬──────────┘    │
│         │             │            │                  │                │
│         ▼             ▼            ▼                  ▼                │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │ detection.Anomaly  (Signal: metrics|logs|events|security)     │    │
│  └───────────────────────────────┬──────────────────────────────┘    │
│                                   ▼                                    │
│                          ┌─────────────────┐                          │
│                          │   Correlator    │  ◀── security events      │
│                          │  (window+dedup) │      correlacionam com    │
│                          └────────┬────────┘      anomalias do mesmo   │
│                                   │               pod na janela        │
│                                   ▼                                    │
│                    ┌──────────────────────────┐                       │
│                    │ Enrichment + Severity     │                       │
│                    │ escalation (falco prio)   │                       │
│                    └────────────┬─────────────┘                        │
│                                 ▼                                      │
│                          ┌─────────────┐                              │
│                          │ Dispatcher  │ → Alertmanager (existente)    │
│                          └─────────────┘                              │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
                  ┌────────────────┴─────────────────┐
         (A) Loki: Falco→Falcosidekick→OTel→Loki   (B) webhook: Falcosidekick→HTTP
            controller faz LogQL pull                  POST /webhooks/falco
```

## Componentes

| Componente | Responsabilidade | Localização |
|-----------|-----------------|-------------|
| `Source` (interface) | Contrato de fonte Falco: emite `Event` num canal; abstrai pull vs push | `internal/falco/source.go` |
| `LokiSource` | Implementação **pull** (decisão A): consulta logs do Falco via LogQL no `LogsPoller` existente, parseia JSON, emite eventos | `internal/falco/loki_source.go` |
| `WebhookSource` | Implementação **push** (decisão B): HTTP receiver que recebe POST do Falcosidekick e emite eventos | `internal/falco/webhook_source.go` |
| `Event` | Struct normalizada do evento Falco (priority, rule, source, output_fields → identidade) | `internal/falco/event.go` |
| `Normalizer` | Mapeia `Event` → `detection.Anomaly` (Signal="security"), extrai identidade via `correlation.ExtractWorkload`, aplica `min_priority` | `internal/falco/normalize.go` |
| `Config` | Struct `falco.*`, participa do hot-reload via `config.Watcher` | `internal/falco/config.go` |
| Hook no `Correlator` | Aceitar sinal `security`, correlacionar por pod na janela, escalar severidade | `internal/correlation/correlator.go` (extensão) |

## Fluxo de execução

```
1. Falco Source (A: LokiSource poll loop | B: WebhookSource HTTP handler)
   produz raw Falco events
2. Normalizer:
   a. SE priority < falco.min_priority → drop + metric(dropped{reason=low_priority})
   b. Extrai namespace/pod de output_fields (k8s.ns.name, k8s.pod.name)
   c. workload = correlation.ExtractWorkload(pod)
   d. Monta detection.Anomaly{Signal:"security", Detector:"falco",
      Severity: mapPriority(priority), Labels:{namespace, pod, workload,
      falco_rule, falco_priority, falco_source}, Timestamp: event.time}
3. Anomaly entra no Correlator (mesmo canal das outras fontes)
4. Correlator:
   a. Agrupa por namespace/pod na janela
   b. SE existe anomalia non-security para o mesmo pod na janela →
      merge: adiciona "security" a Signals, anexa falco context
   c. SE prioridade Falco ≥ critical_threshold → escala Severity = "critical",
      seta annotation escalation_reason="falco_correlation"
   d. SE só há sinal security e nenhuma anomalia correlacionada (v1) →
      NÃO emite alerta; metric(correlations{result=security_only_suppressed})
5. Enrichment + LinkBuilder adicionam falco_* annotations + link Loki
6. Dispatcher → Alertmanager (existente, inalterado)
```

## Modelo de ingestão: decisão A vs B

Ver **Rationale Decisão 1**. Resumo das duas implementações por trás da interface
`Source`:

### (A) Loki pull — LogQL
```
Falco → Falcosidekick (loki output) → OTel Collector → Loki
controller: LogsPoller.QueryMetricRange / query LogQL a cada falco.poll_interval
LogQL: {app="falco"} | json | priority=~"Critical|Alert|Emergency"
```
- Reusa `internal/ingestion/logs.go` (já testado em prod).
- Respeita `observability-principles.md`: nada de export direto para backend; Falco
  já manda pro Collector→Loki.
- Latência = poll interval (ex: 15-30s). Aceitável para correlação in-window.

### (B) Webhook push — Falcosidekick HTTP output
```
Falco → Falcosidekick (webhook output) → POST controller:8090/webhooks/falco
```
- Tempo-real (sub-segundo).
- Cria endpoint HTTP de entrada no controller → **superfície de rede nova** (precisa
  auth + flag de segurança — ver Security Considerations).
- Não passa por Loki, então o evento não fica naturalmente pesquisável (perde o link
  Loki "grátis" do A).

## Event schema (normalizado)

```go
// Event is the normalized Falco event consumed internally.
type Event struct {
    Time      time.Time
    Priority  string // Emergency|Alert|Critical|Error|Warning|Notice|Informational|Debug
    Rule      string // e.g. "Terminal shell in container"
    Source    string // "syscall" | "k8s_audit"
    Namespace string // from output_fields k8s.ns.name
    Pod       string // from output_fields k8s.pod.name
    Container string // from output_fields container.id (annotation only, NOT a metric label)
    Output    string // human-readable message (annotation only)
}
```

Falcosidekick/Falco JSON de origem (campos relevantes): `priority`, `rule`,
`source`, `time`, `output`, `output_fields.k8s.ns.name`, `output_fields.k8s.pod.name`,
`output_fields.container.id`.

## Configuração

```yaml
falco:
  enabled: false              # hot-reloadable via config.Watcher
  mode: "loki"                # "loki" (pull, decisão A) | "webhook" (push, decisão B)
  min_priority: "Warning"     # descarta abaixo disso na ingestão
  critical_priority: "Critical" # prioridade >= isso escala alerta para critical

  # mode=loki
  loki_query: '{app="falco"} | json'
  poll_interval: 30s

  # mode=webhook
  listen_addr: ":8090"
  webhook_path: "/webhooks/falco"
  auth_token_file: "/etc/secrets/falco-webhook-token"

  # correlação
  correlation_window: 5m      # janela para casar evento Falco com anomalia (pode > janela padrão)
```

## Métricas expostas

| Métrica | Tipo | Labels | Descrição |
|---------|------|--------|-----------|
| `staffops_ad_falco_events_total` | Counter | `priority,namespace,source` | Eventos Falco ingeridos (pós-filtro) |
| `staffops_ad_falco_events_dropped_total` | Counter | `reason={low_priority,parse_error,no_identity}` | Eventos descartados na ingestão |
| `staffops_ad_falco_correlations_total` | Counter | `result={correlated,escalated,security_only_suppressed}` | Resultado da correlação |
| `staffops_ad_falco_ingestion_errors_total` | Counter | `source={loki,webhook}` | Falhas de ingestão (stream/poll down) |

**Cardinalidade**: priority(~5 efetivos pós-filtro) × namespace(~50) × source(2) ≈ 500
séries para `events_total`. Dentro do limite (≤2000). `rule`, `pod`, `container`,
`output` **nunca** viram label — vão para anotações do alerta e logs estruturados.

## Rationale

### Decisão 1: Ingestão via Loki pull (modo A) como default; webhook (B) opt-in

**Escolha**: Implementar ambos atrás de uma interface `Source`, mas o **default é
`mode: loki`** (pull via LogQL). Webhook é opt-in para quem precisa de tempo-real.

**Justificativa, em ordem de força**:
1. **Reuso + menor superfície**: o `LogsPoller` (LogQL) já existe, está testado em
   prod e não abre porta nova de rede. Webhook cria um endpoint HTTP de entrada — nova
   superfície de ataque que exige auth, e que o steering manda flaggar explicitamente.
2. **Aderência ao `observability-principles.md`**: Falco→Collector→Loki é o fluxo
   canônico; o controller só consulta. Webhook recebe direto do Falcosidekick,
   bypassa o pipeline de telemetria.
3. **Link de investigação grátis**: se o evento já está no Loki, o `LinkBuilder` gera
   um deep-link LogQL para o operador ver o evento original. No modo webhook isso
   exigiria persistir o evento em algum lugar.

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Latência de poll (15-30s) no modo Loki | Correlação é in-window (minutos); 30s é irrelevante para casar com saturação de recurso |
| Duas implementações para manter | Interface `Source` é fina (~1 método); webhook é valioso para casos de cryptominer onde segundos importam. Custo baixo, opção preservada |
| Depende do Falcosidekick estar com output Loki configurado | Pré-req de infra já assumido (gitops/security) |

**Quando essa decisão estaria errada**:
- Se o caso de uso dominante exigir resposta em <5s (ex: bloquear ataque ativo) — aí
  webhook vira default. Mas v1 é observação/enriquecimento, não resposta.
- Se o volume de eventos Falco no Loki for alto a ponto de o poll LogQL ficar caro
  (muitos namespaces × alta frequência) — reavaliar push.

**Alternativa descartada**: Falco gRPC Outputs API consumida direto pelo controller
(streaming nativo). Descartada porque acopla o controller à API gRPC do Falco
(versionada, muda entre releases), exige descoberta/conexão a cada pod Falco do
DaemonSet, e duplica o que o Falcosidekick já faz como fan-out. Falcosidekick é o
ponto de integração projetado para isso.

### Decisão 2: Falco enriquece, não dispara alerta isolado (v1)

**Escolha**: Evento Falco sem anomalia de recurso/latência correlacionada **não**
gera alerta. Só agrega valor quando casa com um sinal existente.

**Justificativa, em ordem de força**:
1. **Não duplicar o Falcosidekick**: o Falco já tem seu próprio alerting (Slack,
   PagerDuty via sidekick). Emitir alerta security-only pelo nosso pipeline criaria
   alerta em dobro para o mesmo evento.
2. **O diferencial do nosso sistema é a correlação cross-signal**: "anômalo EM
   recurso E suspeito EM runtime" é o sinal de alto valor que ninguém mais produz.
   Falco sozinho o Falcosidekick já cobre.
3. **Controle de ruído**: Falco em cluster grande dispara muitos eventos
   Notice/Informational. Tratar todos como alerta seria flood.

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Evento Falco crítico isolado (sem saturação) não vira alerta nosso | O Falcosidekick já alerta sobre ele; não perdemos cobertura, só não duplicamos |
| Janela de correlação pode "perder" eventos que precedem a saturação | Mitigado pela `correlation_window` maior/assimétrica (Decisão 3) |

**Quando essa decisão estaria errada**:
- Se operadores quiserem o enriquecimento/links do nosso pipeline também para eventos
  Falco standalone (não só correlacionados). Aí abrir v2: "security-only alerts" com
  flag dedicada. Registrado em Fora de escopo.

### Decisão 3: Janela de correlação de segurança potencialmente assimétrica

**Escolha**: `falco.correlation_window` é configurável e pode ser **maior** que a
janela de correlação padrão, com viés para o passado (evento Falco *precede* a
saturação).

**Justificativa, em ordem de força**:
1. **Mecânica causal real**: em ataques (ex: cryptominer), a ação suspeita (shell,
   download de binário) ocorre **antes** do efeito observável (CPU spike). Uma janela
   simétrica curta perderia a relação causa→efeito.
2. **Falso-negativo é caro em segurança**: perder a correlação que transformaria um
   "pod sob carga" em "comprometimento" é o pior erro possível aqui.

**Trade-offs aceitos**:
| Custo | Realidade |
|-------|-----------|
| Janela maior aumenta chance de correlação espúria (evento não relacionado) | Aceitável: a anotação deixa claro que é correlação temporal, não causalidade provada; operador julga. E a escalada exige prioridade alta do Falco |
| Mais estado em memória no correlator (janela maior = mais pending) | Bounded por namespace/pod; impacto pequeno vs benefício |

**Quando essa decisão estaria errada**:
- Se a taxa de correlação espúria (escaladas que o operador marca como FP) for alta —
  encurtar a janela. Observável via `falco_correlations_total{result=escalated}` vs
  feedback (P3.3 futuro).

## Invariantes

- Falco source indisponível NUNCA bloqueia nem derruba o pipeline das 3 fontes existentes
- Falco NUNCA é processado se `falco.enabled == false`
- Métricas `staffops_ad_falco_*` NUNCA usam `rule`/`pod`/`container`/`output` como label
- Eventos abaixo de `min_priority` NUNCA chegam ao correlator (filtro na ingestão)
- Em v1, sinal `security` isolado NUNCA emite alerta sozinho (só correlacionado)
- Severidade SÓ escala para cima por correlação Falco, nunca rebaixa
- Token do webhook (modo B) NUNCA aparece em logs ou métricas
- A identidade do evento (namespace/pod/workload) usa a MESMA extração
  (`correlation.ExtractWorkload`) das outras fontes — consistência de agrupamento
- Anotação de correlação deixa explícito "temporal correlation", não afirma causalidade

## Dependências externas

| Serviço | Propósito | Criticidade |
|---------|-----------|-------------|
| Falco (DaemonSet) | Produz eventos de runtime security | Soft (fonte opcional; ausência degrada graciosamente) |
| Falcosidekick | Fan-out dos eventos (output Loki ou webhook) | Soft |
| Loki (modo A) | Armazena/serve eventos Falco via LogQL | Hard no modo A (já é dep existente) |
| Alertmanager | Canal primário de alerta | Hard (existente) |
| Redis | Dedup de correlação (existente) | Hard (existente) |

## Security Considerations

- **Modo webhook (B) cria endpoint HTTP de entrada** — exige `auth_token_file`
  (bearer token via volume mount, padrão 12-factor/cloud-security steering). Sem token
  configurado, o modo webhook SHALL recusar inicializar (fail closed). Flaggar
  explicitamente: este é um serviço de rede novo exposto.
- **Validação de input**: payload do webhook é input externo não-confiável — validar
  schema, limitar tamanho do body, rejeitar malformados (não crashar).
- **Modo Loki (A) não abre porta** — preferível do ponto de vista de superfície.
- Eventos Falco podem conter dados sensíveis no `output` (paths, comandos) — tratados
  como anotação/log, nunca como label de métrica; não logar em nível que exponha
  amplamente.

## SRE Review Notes

> Pendente. Esta spec foi escrita pelo orchestrator a partir do código real. Antes da
> implementação, delegar revisão ao subagent `sre` (janela de correlação, blast radius
> da fonte Falco, impacto no `cycle_duration_seconds`) e ao subagent `security`
> (superfície do webhook, manejo de token, dados sensíveis no output Falco). Seguir o
> padrão de "Findings & Changes Applied" da spec `agent-api-integration`.
