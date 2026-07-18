# Signal Definition Methodology (detecção assertiva sem IA)

> Template de orientação: **como decidir o que monitorar, com qual detector, com
> quais métricas de qualidade e a que custo** — antes de escrever qualquer regra.
> Nenhuma técnica aqui depende de ML/IA: é estatística clássica + semântica de
> sinal + disciplina de custo. A integração com agentes (aigent-squad, P5.5) fica
> **opcional por cima** de um detector que já é bom sozinho.

---

## Princípios (o "porquê" antes do "o quê")

1. **Alerte sintoma, não causa.** O usuário sente latência/erro/indisponibilidade.
   Causas (CPU, GC, pool) são *contexto de enriquecimento* e *sinais antecedentes* —
   não páginas independentes. Um incidente = um alerta com a cadeia dentro.
2. **Todo sinal precisa de um dono e uma ação.** Se ninguém faz nada quando dispara,
   não é um alerta — é ruído com timestamp. A ação entra na definição, não depois.
3. **O detector não sabe o que o sinal significa — você declara.** Z-score trata
   "taxa de request caiu" igual a "taxa de erro subiu". A semântica (direção,
   classe, teto) tem que ser metadado da regra, não conhecimento na cabeça de alguém.
4. **Cada série adaptativa tem custo triplo**: memória Redis (baseline), carga de
   query no Prometheus, e orçamento de falso positivo. Série que não justifica os três, sai.
5. **Nenhuma mudança de detector sem número.** O harness de medição (P0.1) é o
   pré-requisito: toda técnica desta página é avaliada por recall/FP sobre falhas
   injetadas, não por argumento.

---

## O catálogo de sinais (template — preencha um por sinal)

Todo sinal monitorado declara este bloco (vive junto da regra no `config.yaml` ou
em `docs/detection/catalog/`):

```yaml
signal:
  name: http_error_rate            # nome estável, snake_case
  query: 'sum(rate(...)) by (service_name)'
  class: errors                    # latency | errors | traffic | saturation (RED/USE)
  kind: ratio                      # counter_rate | gauge | ratio | quantile
  direction: up_bad                # up_bad | down_bad | both_bad
  bounded:                         # o sinal tem teto físico conhecido?
    ceiling: null                  # ex.: 100 (percent), max_connections, null = sem teto
  role: lagging                    # leading | lagging (do degradation-model.md)
  seasonality: daily               # none | daily | weekly  (define se seasonal profile aplica)
  scope: [service_name, namespace] # identidade estável (NUNCA pod cru — ExtractWorkload)
  cardinality_estimate: 40         # nº de séries que o group_by gera (revisar se >100)
  action: >                        # o que o operador FAZ quando dispara
    Checar deploy recente; se não houve, abrir runbook X seção Y.
  runbook: ${RUNBOOK_BASE_URL}/http-error-rate
  fp_budget_per_week: 2            # quantos falsos positivos/semana são toleráveis
  consumer: on-call-sre            # quem recebe (se "ninguém", não crie a regra)
```

**Regra de admissão**: sinal sem `action` + `consumer` + `fp_budget` preenchidos
não entra em produção. Sinal novo passa por replay (`--replay`) e, quando P0.1
estiver ativo, por injeção sintética antes do primeiro deploy da regra.

## Árvore de decisão: qual detector para qual sinal

| Se o sinal... | Detector | Por quê |
|---------------|----------|---------|
| Tem teto/limite conhecido (`bounded.ceiling`) | **static** (`value OP threshold`) | Determinístico, zero custo de baseline, zero FP estatístico |
| Cresce previsivelmente rumo a um teto (fila, disco, pool) | **`predict_linear` em PrometheusRule** — fora deste produto | Saturação rumo a teto conhecido é *previsível*, não anômala (Decision 8) |
| Tem SLO definido | **burn-rate multi-window em PrometheusRule** — fora deste produto | Padrão da indústria, assertivo por construção; consumir, não reconstruir |
| Baseline desconhecido + sazonalidade estável | **adaptive** (EWMA/seasonal) | O único caso que justifica o custo do baseline |
| É textual/padrão em log (panic, OOM) | **pattern** (LogQL rate) | Sem baseline numérico |
| DEVE sempre emitir (heartbeat, exporter) | **absence** (P2.10, quando existir) | Silêncio é o sinal |

> Anti-padrão nº 1: colocar no `adaptive` o que cabe em `static`/`predict_linear`.
> Cada série adaptativa desnecessária consome os três orçamentos à toa.

## Técnicas de assertividade sem IA (menu priorizado)

Ordenadas por relação valor/custo. As marcadas 🔒 são detector-core → **bloqueadas
até P0.1 rodar** (ROADMAP Phase 0); as demais podem antes/em paralelo.

| # | Técnica | O que resolve | Custo |
|---|---------|---------------|-------|
| 1 | **Change-aware suppression** — janela de supressão/rebaixamento pós-rollout (via K8s events de deploy/scaling, já ingeridos em `ingestion/events.go`) | A maior fonte de FP em k8s: todo deploy gera anomalia *esperada* de CPU/restart/latência | Pequeno; não é detector-core |
| 2 | **Direction-of-badness** — anomalia só na direção declarada no catálogo | FP em melhorias (latência caiu, tráfego caiu de madrugada) | Pequeno; metadado + 1 if |
| 3 | **FDR (Benjamini-Hochberg)** por ciclo (P0.4) | ~400 séries × z>3 = ~1000 FP/dia por comparações múltiplas | Pequeno; pós-detecção, pré-dispatch |
| 4 | 🔒 **Persistência N-de-M** (ex.: 3 de 5 ciclos) antes de disparar; histerese para resolver | Hoje um único sample decide (`adaptive.go`); spikes de 60s viram alerta | Pequeno; +latência de detecção ~N ciclos (aceitável: página-se sintoma sustentado) |
| 5 | 🔒 **Estatística robusta** — median/MAD em vez de mean/stddev no z-score | Outlier contamina o baseline que julga o próximo sample; mitiga poisoning (P2.9) de graça | Médio; muda armazenamento do baseline |
| 6 | 🔒 **CUSUM/Page-Hinkley** para rampas | Cegueira-a-rampa documentada (EWMA α=0.3 persegue a subida — Decision 8.4) | Médio; por série, O(1) memória |
| 7 | **SLO-aware severity** (P3.4) | Severidade vira função de impacto real, não só de desvio estatístico | Médio; precisa catálogo de SLO |

**O que NÃO fazer** (reafirmando Decision 8 e a hipótese): multivariado sobre todas
as séries, deep learning de séries temporais, troca do engine "porque existe
algoritmo melhor" sem número do P0.1 antes e depois.

## Métricas de qualidade do detector (o que medir para saber se está bom)

### Por regra/sinal (revisar semanal)

| Métrica | Fonte | Alvo inicial |
|---------|-------|--------------|
| Precision proxy | feedback loop (P3.3) ✅/❌ | > 0.7 (páginas); > 0.5 (tickets) |
| Alertas/semana | `staffops_ad_alert_fired_total` por regra | ≤ `fp_budget_per_week` do catálogo |
| Recall por tipo de falha | injeção sintética (P0.1): spike/ramp/step/silence | declarar por regra; ramp é o teste ácido |
| Time-to-detect | P0.1: injeção → primeiro alerta | < 3 ciclos (180s) para leading signals |
| Actionability | % de alertas que geraram ação (feedback) | > 50%; abaixo disso a regra vira ticket ou morre |

### Globais do sistema (dashboard operator já existe — adicionar o que falta)

| Métrica | Por quê |
|---------|---------|
| FP/dia total (estimado via feedback + dedup) | O orçamento global de atenção humana |
| `alert_deduplicated_total / alert_fired_total` | Dedup alto = regra gritando repetido |
| `baseline_series_tracked` | Custo Redis; guarda de cardinalidade (P5.4) em 10k |
| Queries/ciclo e duração p99 do ciclo | Custo Prometheus; rate limits 20/s Prometheus já existem |
| % anomalias suprimidas por janela pós-deploy | Mede o valor da técnica nº 1 |

### Ciclo de vida de uma regra

```
proposta (catálogo completo) → replay sobre 7d históricos → injeção sintética
→ dry-run 1-2 semanas (observar volume) → ativa → revisão semanal contra fp_budget
→ 2 semanas estourando budget sem ação → demote (info) ou remove
```

## Modelo de custo (para não estourar)

- **Tier 0 — de graça**: static rules e PrometheusRules (`predict_linear`, burn-rate) rodam
  no Prometheus que já existe. Use amplamente.
- **Tier 1 — barato**: pattern (LogQL) — custo por query; agrupe por namespace.
- **Tier 2 — caro**: adaptive — Redis + queries por ciclo + FP budget. **Curado**:
  só sinais do catálogo com justificativa; alvo < 500 séries adaptativas.
- **Empurre agregação para recording rules**: se a query do sinal agrega muito,
  crie recording rule no Prometheus (`vmrules.yaml`) e consulte a série pré-agregada — a
  conta roda 1× no Prometheus, não a cada worker/ciclo.
- **Sem custo de LLM**: nada nesta página chama IA. A integração aigent-squad
  (P5.5) permanece **opcional e atrás de flag**, consumindo alertas já assertivos.

## Sequenciamento honesto

1. **Agora / paralelo à Phase 0**: catálogo dos sinais existentes (preencher o
   template acima para cada regra do `config.yaml` — vai revelar regras sem ação ou
   sem dono), técnicas 1-3 (suppression pós-deploy, direction, FDR).
2. **Depois do P0.1 dar números**: técnicas 4-6, cada uma medida antes/depois pelo
   harness de injeção — é isso que transforma "acho que melhorou" em "recall(ramp)
   subiu de X para Y com FP igual".
3. **Depois de alertas reais + feedback (P5.3/P3.3)**: SLO-aware severity, tuning
   por precision real, e só então a integração de agentes como camada opcional.
