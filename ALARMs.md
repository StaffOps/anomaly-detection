# ALARMs — autonomous observation log

> **Temporary file.** Records findings during loop monitoring of the running stack at `controller 0.7.0` / `ml 0.2.0` (dry-run mode, docker-compose, external endpoints).
>
> Promoted findings should be moved to `ROADMAP.md` or steering as appropriate, then this file deleted.

## Format

```
## YYYY-MM-DD HH:MM — short title
- **Type**: observation | warning | bug | tuning_needed | working_as_designed
- **Detail**: <what was noticed>
- **Evidence**: <metric / log line / cmd output>
- **Action**: <suggested follow-up | none>
```

---

## 2026-05-30 19:55 — workers_available gauge stuck at 0
- **Type**: bug
- **Detail**: `staffops_ad_controller_workers_available = 0`, mas os 3 workers estão de fato rodando e processando jobs (126 dispatched ok). O gauge nunca é atualizado no código — definido em `metrics.go` mas nenhum lugar faz `.Set(N)`.
- **Evidence**: `docker ps` mostra worker-1/2/3 healthy, jobs sendo processados, mas a gauge permanece 0
- **Action**: setar a gauge periodicamente (ex: ping aos workers ou contagem do round_robin LB). Bug real, baixa prioridade.

## 2026-05-30 19:55 — ML multivariate com ~10% de erro
- **Type**: warning
- **Detail**: `ml_calls_total{status="error"} = 6` em 62 calls totais (~9.7%). Não tem nenhum log warning visível pra explicar. Possível timeout, payload malformado, ou Python crash transient.
- **Evidence**: `staffops_ad_controller_ml_calls_total{method="multivariate",status="error"} 6` vs `status="ok" 56`
- **Action**: aumentar log level no client.go quando ML retorna erro (hoje só `slog.Warn`); inspecionar logs do ml-1.

## 2026-05-30 19:55 — Enrichment cache 0 hits em 62 calls
- **Type**: tuning_needed
- **Detail**: `cache_hits = 0`, `cache_misses = 62`. Cada anomalia bate em workload diferente. Cache TTL é 30s mas correlator dedup também é 5min, então mesmo workload não enriquece de novo dentro do dedup — e fora do dedup, baseline já mudou. Cache nunca terá hit no padrão atual.
- **Evidence**: 62 misses, 0 hits após 14 ciclos (~7min)
- **Action**: ou (a) remover cache (não está adicionando valor), ou (b) chave por `namespace+pod` independente do anomaly metric (já é o que faz, então provavelmente cada workload anomalia só 1x), ou (c) aumentar TTL pra 2-5min. Investigar antes de bumpar.

## 2026-05-30 19:55 — Service-level anomalies colapsam em "" / "" workload key
- **Type**: bug
- **Detail**: Anomalies de `latency_p99_by_service` e `error_rate_by_service` têm `namespace=""` e `pod=""` (rótulos vazios). O correlator usa `workloadKey = namespace + "/" + pod` → todas as services anomalies caem no mesmo grupo `"/"`, virando 1 alerta correlacionado representando N services diferentes.
- **Evidence**: 4 service anomalies num ciclo (Pricing, Metadata, Views, classifyid) viram 1 enrichment run (`enrichment_runs_total{kind="service"} = 1`).
- **Action**: ajustar `workloadKey()` em `correlator.go` pra usar `service_name` quando pod está vazio. Senão alertas distintos somem em um.

## 2026-05-30 19:55 — Readiness check metrics ausentes do scrape
- **Type**: warning
- **Detail**: `staffops_ad_controller_readiness_checks_total` não aparece mais no `/metrics`. Mais cedo (depois do build do 0.8.0 original) aparecia. Suspeita: as readiness checks só incrementam o counter quando `/readyz` é chamado, e ninguém está chamando o endpoint. Funcional, mas sem visibilidade.
- **Evidence**: grep retornou vazio em `/metrics`
- **Action**: confirmar — chamar `/readyz` algumas vezes e ver se a métrica aparece.

## 2026-05-30 19:55 — Volume alto de criticals (174) ~ paritário com warnings (189)
- **Type**: tuning_needed
- **Detail**: Quase metade dos anomalies são `critical`. Em sistema healthy, criticals deveriam ser raros. A escalação automática (warning + multi-signal → critical no correlator, e ML confirms → critical) pode estar agressiva demais.
- **Evidence**: 174 critical_metrics + 6 critical_logs vs 189 warning_metrics + 17 warning_logs em 14 ciclos
- **Action**: amostrar alertas críticos e checar se faria sentido pra um operador acordar às 3am. Se não, downgrade severity escalation criteria.

## 2026-05-30 19:58 — Alertmanager check passou a falhar / /readyz=503
- **Type**: warning
- **Detail**: `/readyz` retornando 503 consistentemente (5/5). Causa: `readiness_checks_total{dependency="alertmanager",result="error"} 5`. VM e Loki ok. Earlier (no 0.8.0 rebuild) AM passava. Pode ser timeout transitivo, mudança de endpoint, ou algo no AM.
- **Evidence**: 5/5 503s, only AM falhou; VM e Loki nos mesmos 5 calls retornaram ok
- **Action**: testar `wget` direto pro AM dentro do container; se for transient, esperar; se persistir, ajustar `AlertmanagerChecker` (talvez `/api/v2/status` mudou ou tem auth?). **Importante**: stack rodando "ready=true" inicialmente, agora passou a "not ready" — anti-flapping logic não existe.

## 2026-05-30 19:58 — ML feature count mismatch (BUG IMPORTANTE)
- **Type**: bug
- **Detail**: ML Python errors com `ValueError: X has 3 features, but IsolationForest is expecting 6 features as input`. O Python `multivariate.py` usa UM modelo Isolation Forest único, fitted com N features uma vez, depois rejeita inputs com N diferente.
- **Causa raiz**: pod-level enrichment dá 6+ features (anomaly_score, anomaly_value, cpu_ratio, memory_ratio, restarts_5m, ready_replicas...). Service-level dá 3-5 (anomaly_score, anomaly_value, error_rate_1m, request_rate_1m, latency_p99_5m). Modelo é fitted com primeiro batch (digamos pod=6), aí service request=5 features → fail.
- **Evidence**: `staffops_ad_ml_requests_total{method="DetectMultivariate",status="error"} 6.0`; traceback no log do ml-1
- **Action**: opções:
    1. Padding com zero pra schema fixo (lista canônica de features) no Python ou Go
    2. Modelos separados por kind ("pod_model", "service_model")
    3. Refit sempre que feature count muda (caro mas simples)
  Recomendo (2) — múltiplos modelos no Python `MultivariateDetector`. **Necessita fix antes de tirar dry-run**.

## 2026-05-30 19:58 — 306 anomalies no workload-key vazio "/"
- **Type**: bug (já registrado em "Service-level colapsam") — agora com evidência quantitativa
- **Detail**: Top noisy workloads mostra 306 anomalies em "/" (namespace+pod vazios) vs 4 max em qualquer pod específico. Confirma o bug: todos os service-level anomalies viram um único "workload" no correlator, sendo deduplicados como se fosse um pod só.
- **Evidence**: `docker logs ... | jq '.namespace + "/" + .pod' | sort | uniq -c` → 306 para "/"
- **Action**: fix em `correlator.go workloadKey()` — usar service_name como fallback quando ns/pod estão vazios.

## 2026-05-30 19:58 — Workloads infra noisy: vm-cluster-vmselect, ztunnel, istio-cni
- **Type**: tuning_needed
- **Detail**: VictoriaMetrics próprio aparece como anomalous (4 anomalies em 30min para vmselect-2 e vmselect-0). Mesmo padrão: ztunnel, istio-cni-node, fluent-bit. São pods de infra com workload variável que disparam adaptive falsamente.
- **Evidence**: `monitoring/vm-cluster-vmselect-{0,2}` = 4 cada; `istio-system/ztunnel-*` e `istio-cni-*` = 3 cada
- **Action**: adicionar `monitoring`, `istio-system` ao `exclude_static_only` ou criar uma terceira lista `exclude_high_variance` (suprime adaptive mas mantém critical static). Decidir após observar mais.

## 2026-05-30 20:01 — Alertmanager flap (5 erros → 1 ok depois)
- **Type**: working_as_designed (mas com gap)
- **Detail**: AM saiu de error=5 pra mostrar 1 ok subsequente — era transient. `wget` direto pro AM dentro do container retornou status normal. Mas isso significa que **`/readyz` flapa** baseado em transients de upstream — não tem retry/multiple-failure-threshold. Pod K8s seria ejetado de Service em situação assim.
- **Evidence**: error=5, depois 1 ok no mesmo período.
- **Action**: implementar "fail só após N erros consecutivos" no `metrics.Server.handleReadyz`, ou wrap cada checker com tolerância. Não-bloqueante mas afeta usabilidade em prod.

## 2026-05-30 20:01 — Cycle duration alta: avg 11.2s, p99 > 10s
- **Type**: warning
- **Detail**: 23 cycles, sum 257.7s → avg 11.2s. Mas `le="10" bucket = 13` → significa que 10 ciclos excederam 10s. Cycle interval é 30s, então gastando ~37% wall clock só processando. Em prod com mais workloads o ciclo pode estourar o intervalo.
- **Evidence**: `cycle_duration_seconds_count=23, sum=257.7, le="5"=0, le="10"=13`
- **Action**: profilar o ciclo. Provavelmente o gargalo são queries VM/Loki (3 jobs * N pods cada) + enrichment (5-7 queries por alerta). Considerar paralelizar fan-out de jobs entre workers, ou aumentar `controller.job_interval` quando há muitos workloads.

## 2026-05-30 20:01 — ML zero detections em 109 calls
- **Type**: warning (relacionado ao bug do feature count)
- **Detail**: `staffops_ad_ml_multivariate_anomalies_total = 0`. Em 109 calls bem-sucedidas, IsolationForest nunca detectou anomalia. Combinando com os 11 erros (feature count mismatch), suspeita: modelo refita constantemente entre pod-shape e service-shape, nunca acumula histórico estável o bastante pra fazer fit confiável.
- **Evidence**: 109 ok, 0 anomalies; 11 errors com mismatch
- **Action**: depende do fix do bug de feature count (ALARMs anterior). Sem schema estável, ML não vai funcionar. Bloqueador de valor real do P2.1.

## 2026-05-30 20:01 — Volume de anomalies crescendo linearmente: 27/min sustained
- **Type**: tuning_needed
- **Detail**: De 14→23 cycles (3min): anomalies cresceram 174→203 critical_metrics (+29) e 189→286 warning_metrics (+97). Total ~80 anomalies em 3min = 27/min, ~13 por ciclo. Não está estabilizando — baseline ainda em ajuste e/ou detecção realmente sensível demais.
- **Evidence**: deltas das counters em 3min
- **Action**: monitorar mais 30min — se taxa não cair (após warm-up dos baselines completar), threshold zscore=3.0 está muito sensível. Considerar 4.0 ou requerir 2 ciclos consecutivos antes de fire.

## 2026-05-30 20:05 — Stack de observabilidade local subido (Prom + Grafana)
- **Type**: working_as_designed (tooling)
- **Detail**: Adicionados services `prometheus` e `grafana` ao docker-compose. Prometheus scraping 5 targets (controller, ml, 3 workers — todos UP). Grafana provisioned com 3 dashboards (Overview, Detection, ML & Enrichment).
- **Evidence**: `curl /-/ready` OK, `/api/v1/targets` mostra 5 targets up
- **Action**: usar PromQL via API (`http://localhost:9090/api/v1/query`) pra próximas iterações do loop autônomo. Mais preciso que `curl /metrics | grep`.

## 2026-05-30 20:08 — RETRATAÇÃO: workers balanceados (não estavam imbalanced)
- **Type**: observation (correção)
- **Detail**: Achado anterior de "só worker-3 processando" era artefato de Prometheus recém-iniciado sem dados suficientes pra `rate()`. Após 1min de scraping, workers mostram 4/4/9 jobs/min — round-robin saudável. Todos têm ~99 jobs processados, ~11.7k baseline updates.
- **Evidence**: `rate(staffops_ad_worker_jobs_processed_total[2m])` em scripts-worker-1/2/3 todos > 0
- **Action**: nenhuma — sistema funcionando.

## 2026-05-30 20:08 — 🎯 Primeira detecção ML POSITIVA
- **Type**: working_as_designed (positive milestone)
- **Detail**: `staffops_ad_ml_multivariate_anomalies_total = 1`. Apesar dos 33% error rate (feature count bug), o Isolation Forest acumulou histórico suficiente e disparou uma detecção real. O sistema está produzindo valor.
- **Evidence**: counter cumulativo
- **Action**: monitorar quantas detecções ML ocorrem nas próximas horas. Se trickle constante (>5/h), o pipeline está validando. Se zero por horas, ainda há problema sistêmico.

## 2026-05-30 20:08 — ML error rate ~33%
- **Type**: warning (mesmo que o feature count bug, agora com nova evidência)
- **Detail**: ML calls últimos 5min: 2/min error, 4/min ok = ~33% error rate. Pior que o ~10% medido na primeira janela. Provavelmente porque mais service-level alerts agora estão acontecendo (com 3-5 features ≠ 6 features dos pod-level).
- **Evidence**: `rate(staffops_ad_controller_ml_calls_total{status="error"}[5m])`
- **Action**: prioridade — implementar fix de schema fixo (padding com zero pra schema canônico). Bloqueia confiabilidade do P2.1.

## 2026-05-30 20:14 — 🟢 Anomaly rate convergindo (warm-up dos baselines)
- **Type**: working_as_designed (positive)
- **Detail**: Taxa de anomalies caiu drasticamente — de ~27/min sustained no baseline inicial pra ~5.6/min agora (2.8 critical_metrics + 2.4 warning_metrics + 0.4 warning_logs + 0 critical_logs). Significa que EWMA baselines estão convergindo pra valores estáveis e zscore=3.0 começa a ser razoável.
- **Evidence**: `60 * rate(staffops_ad_detection_anomalies_total[5m])` ~5.6 vs ~27 inicial
- **Action**: nenhuma — comportamento esperado. Continuar observando, threshold pode mesmo estar OK.

## 2026-05-30 20:14 — ML positive detections subindo (1 → 3)
- **Type**: working_as_designed
- **Detail**: 3 detecções multivariate positivas em ~10min. Trickle constante validando que o pipeline funciona apesar dos 20% de error rate (downgrade do 33% inicial — provavelmente porque mais alertas pod-level estão acontecendo, com schema de 6 features consistente).
- **Evidence**: `staffops_ad_ml_multivariate_anomalies_total = 3`
- **Action**: nenhuma. Quando virmos > 10/h consistente, podemos considerar este P2.1 validado em prod (mesmo dry-run).

## 2026-05-30 20:14 — Bug do "/" workload PIORANDO (348 anomalies em 30min)
- **Type**: bug (já registrado, agora com gravidade quantificada)
- **Detail**: O "/" workload key (collapse de service-level anomalies) está agora com 348 anomalies — 29x maior que o segundo lugar (harbor-jobservice = 12). É o problema dominante de ruído. Cada service distinto (Pricing, Metadata, Views, Custom, Logger, classifyid, etc.) cai todo no mesmo grupo.
- **Evidence**: `docker logs ... | jq | sort | uniq -c` → 348 vs 12 do segundo lugar
- **Action**: **prioridade alta** — fix em `correlator.go workloadKey()` deve usar service_name como fallback. Sem isso, alertas de services distintos somem em 1 alerta diário, perdendo informação valiosa.

## 2026-05-30 20:14 — Enrichment cache CONFIRMADO inútil (0% hit em 30min)
- **Type**: tuning_needed → bug
- **Detail**: 0% de cache hit em todo o tempo de execução. Cada request escreve no cache mas nunca lê. Causa raiz: **dedup do correlator (cooldown 5min) já evita reprocessar a mesma workload key**. Quando uma workload re-anomalia depois de 5min, baseline e contexto mudaram. O cache de enrichment de 30s NUNCA pega — porque a primeira run cria a entry, e depois o dedup do correlator suprime o segundo alerta dentro do TTL.
- **Evidence**: 0 hits em ~250 misses ao longo de 30min
- **Action**: remover o cache (não está agregando valor; só consumindo Redis). Simplifica o código e elimina branch morto.

## 2026-05-30 20:14 — Cycle duration histogram precisa de buckets maiores
- **Type**: bug (instrumentation)
- **Detail**: p50, p95 e p99 todos retornam exatamente 10s. Causa: o histogram usa `prometheus.DefBuckets` que termina em 10s. Qualquer ciclo > 10s cai em `+Inf` mas histogram_quantile retorna o último bucket finito (10s). Não conseguimos ver a cauda real.
- **Evidence**: 3 percentis idênticos = clip do bucket
- **Action**: usar buckets customizados em `metrics.go`: `[1, 2.5, 5, 10, 20, 30, 60]` (cycle interval é 30s, entao 60s já significa cycle stalling).

## 2026-05-30 20:21 — 🟢 ML detections escalando: 5 total (~24/h rate)
- **Type**: working_as_designed (positive milestone)
- **Detail**: Counter pulou de 3 → 5. Rate de últimos 5min = 2 detections. Extrapolando: ~24/h. Não é alto mas é trickle constante validando que o pipeline produz valor real.
- **Evidence**: `staffops_ad_ml_multivariate_anomalies_total = 5`, `increase(...[5m]) ≈ 2`
- **Action**: continuar observando. Quando atingir milestone (ex: 50 detections em 24h sem operador rejeitar como ruído), podemos justificar bump 0.7 → 0.8 com evidência real.

## 2026-05-30 20:21 — Distribuição de feature count: p50=6.7, p99=9.9
- **Type**: observation (inesperado)
- **Detail**: Esperava ver duas modas claras (6 features pra pod, 5 pra service). Mas p99 = 9.9 sugere que algumas calls têm 10+ features. Isso vem de enrichments tipo `error_logs_1m` (Loki) e `oom_kills` que ocasionalmente succeed. Schema é dinâmico (cresce quando Loki funciona). Reforça a tese de que precisamos de schema fixo padded.
- **Evidence**: histogram_quantile output
- **Action**: padronizar — definir lista canônica de features e padding com 0 quando ausente. Resolve tanto o error count mismatch quanto a inconsistência observada.

## 2026-05-30 20:21 — alerts_fired_total=0 em dry-run (instrumentation bug)
- **Type**: bug (instrumentation)
- **Detail**: 30 min de execução, ALERTS_DEDUP'd = 3, mas `alerts_fired_total = 0`. Em modo `--dry-run`, o counter `AlertsFired` nunca incrementa (porque ele é setado dentro de `dispatcher.send()` que é skip'd). Isso impossibilita calcular o dedup ratio em dry-run, e zera todas as métricas relacionadas a output.
- **Evidence**: `increase(staffops_ad_alert_deduplicated_total[30m]) = 3` mas `sum(increase(staffops_ad_alert_fired_total[30m])) = 0`
- **Action**: incrementar AlertsFired ANTES do `if d.dryRun { return nil }`. Conta "would-have-fired" mesmo em dry-run. Trivial fix.

## 2026-05-30 20:21 — Cardinalidade alta de baselines: 1253 séries só de cpu_by_workload
- **Type**: tuning_needed
- **Detail**: Redis tem 1253 séries pra `cpu_by_workload`, vs 38-59 pras outras. CronJobs criam pods efêmeros (cada execução = pod novo), e cada pod novo cria baseline nova. Sem TTL, baselines de pods já mortos acumulam pra sempre.
- **Evidence**: `redis-cli scan baseline:* | uniq` → 1253 cpu, 38-59 outras métricas
- **Action**: implementar TTL nas baselines (ex: 24h sem update → expire). Não urgente — Redis aguenta milhões de keys, mas a cardinalidade infla métricas downstream e desperdiça memória. Ligar com P4.4 (cardinality guard).

## 2026-05-30 20:21 — Detector parity: 70 adaptive + 72 static em 10min
- **Type**: observation
- **Detail**: Os dois detectores estão ~equilibrados. Static está pegando os pods CronJob crônicos (dcp-receitafederal, devops-crons), adaptive está pegando outliers Z-score em workloads ativos. Ambos são valiosos. Sem detecção dominando demais.
- **Evidence**: `rate(worker_detections_total[10m]) * 600` → 70 adaptive vs 72 static
- **Action**: nenhuma — saudável.

## 2026-05-30 20:23 — Loki + Promtail adicionados ao stack
- **Type**: working_as_designed (tooling expansion)
- **Detail**: Stack agora completa metrics + logs. Promtail descobriu 5 containers via Docker SD, Loki ingestindo via JSON parsing (extraindo `level`, `msg`, `time`). Labels disponíveis: container, level, project, service, service_name.
- **Evidence**: queries retornaram dados (`anomaly_detected`, `alert_fired` aparecendo). 4 dashboards no Grafana: Overview, Detection, ML & Enrichment, Logs.
- **Action**: ajustar Loki config — primeiro tentei com `metric_aggregation_enabled: true` que não existe na versão 3.0 (era cogitação antiga), removido. Loki 3.0 usa apenas `allow_structured_metadata` + retention.

## 2026-05-30 20:23 — Resumo: stack de observabilidade local completo
- Acesso: http://localhost:9090 (Prom), http://localhost:3000 (Grafana), http://localhost:3100 (Loki)
- Dashboards: 4 (Overview, Detection, ML & Enrichment, Logs)
- Datasources: Prometheus (default) + Loki (logs)
- Pipeline: docker logs → Promtail (Docker SD) → Loki → Grafana
- Próximo loop pode usar PromQL via 9090 + LogQL via 3100, evita raspar texto.

## 2026-05-30 20:48 — P2.4 implementado: workload-aware correlation
- **Type**: working_as_designed (feature delivered)
- **Detail**: Implementado sibling check no correlator. ExtractWorkload() via regex (Deployment/StatefulSet/DaemonSet) — 15 unit tests passing. CorrelatedAlert ganha Kind (pod|workload), AffectedPods, AffectedReplicas. Quando ≥3 réplicas distintas do mesmo workload disparam no mesmo window, emite 1 workload-level alert e marca pod-level como suprimidos. Threshold configurável via `controller.workload_pattern_min_pods` (default 3).
- **Adjacent fix**: dispatcher agora loga `metric`, `reason` (formatado por detector), `identity` no `alert_fired`. Resolve a queixa anterior de "como saber o motivo do alerta?".
- **Evidence after 5min uptime**: 10 alerts, todos pod-level (cluster saudável, nenhum workload com 3+ réplicas spike simultaneamente). Métricas `workload_patterns_total` e `pod_alerts_suppressed_total` registradas, esperando evento.
- **Action**: continuar observando. Quando virmos cluster sob load real (deploy, batch start, traffic surge), workload patterns devem emergir. ALARM "/" workload key (348 anomalies) deve ser drasticamente reduzido — esses casos eram service-level, mas pod-level com 3+ replicas vão colapsar pra 1 alerta.

## 2026-05-30 20:48 — Próximo: P2.5 (ML feature replica_anomaly_fraction) ou observação
- **Type**: observation (planning)
- **Detail**: Com B implementado, próximo passo natural é alimentar `replica_anomaly_fraction` no ML feature vector (P2.5) — mas só faz sentido quando observarmos B funcionando em prod. Por ora, deixar o stack rodando e ver:
  1. Quantos workload patterns são detectados em 24h
  2. Quantos pod alerts são suprimidos
  3. Se há false-negatives (real bug em 1 pod fica encoberto por workload pattern)
- **Action**: nenhuma imediata. Observar.

## 2026-05-30 21:04 — BDC-specific names removidos do código (audit + fix)
- **Type**: working_as_designed (cleanup)
- **Detail**: Auditoria revelou 45 menções de nomes BDC-específicos espalhadas em código, config, tests, deploy manifests e docs. Tudo movido pra env vars + placeholders genéricos.
- **Mudanças**:
  - `workload_test.go`: BDC pod names → genéricos (`my-app-...`, `datastore-0`, `node-agent-...`)
  - `workload.go`: comentário de exemplo agnóstico
  - `config.yaml`: regex `namespace!~"kube-system|gitlab-runner"` → `${EXCLUDE_NAMESPACES_REGEX:kube-system}`; suppression lists viraram `${EXCLUDE_NAMESPACES_CSV}` e `${EXCLUDE_STATIC_ONLY_CSV}`
  - `Suppression` struct refatorado: aceita CSV string nos YAML fields, parsing em `setDefaults`
  - `.env.example`: novo `CLUSTER_NAME=my-cluster`, com seção dedicada de suppression env vars
  - `scripts/.env` (gitignored): valores reais BDC mantidos só localmente
  - `docker-compose.yaml`: novas vars no anchor `x-app-env`
  - Deploy manifests: `bdc-eks-prd` → `REPLACE_ME_CLUSTER_NAME`
  - `README.md` (root + controller): exemplos com `${VAR}` placeholders, comentários genéricos
  - **Deletados**: `controller/config/config.local.yaml`, `scripts/config.local.yaml`, `scripts/docker-compose.local.yaml`, `scripts/start-local.sh`, `scripts/stop-local.sh`, `scripts/monitor-local.sh` (legacy, redundantes)
- **Audit final**: `grep -rE "dpm-|dcp-|bdc|gitlab-runner|scaleops|receitafederal|lawsuits|electoraldata" --include="*.go" --include="*.yaml" --include="*.json" --include="*.py"` → zero matches em código.
- **Mantidos com nomes reais (intencionalmente)**: `scripts/.env` (gitignored, runtime local), `ALARMs.md` (registro factual de observação), `ROADMAP.md`/`CHANGELOG.md` (histórico).


---

## 2026-05-30 23:20 — ✅ RESOLVED: workers_available gauge stuck at 0
- **Type**: resolved
- **Detail**: Wired in `cmd/controller/main.go` main loop using `workerConn.GetState()` — sets gauge to 1 if Ready/Idle, 0 otherwise. Documented as connectivity flag (not actual count) until P5 service discovery brings real backend counting. Validated in stack: gauge = 1 with cluster=bdc-eks-prd label.
- **Evidence**: `staffops_ad_controller_workers_available{cluster="bdc-eks-prd"} 1`
- **Action**: closed (uncommitted in working tree). Part of P4.A.1.

## 2026-05-30 23:20 — ✅ RESOLVED: alerts_fired_total stuck at 0 in dry-run
- **Type**: resolved
- **Detail**: Increment was inside `dispatcher.send()` which is skipped when `dryRun=true`. Moved increment to BEFORE `if d.dryRun` check in both `Fire` and `FireCorrelated`. Counter now measures intent (alerts that would have fired) rather than delivery success — dispatch failures still tracked separately via `AlertsDispatchErrors`. Validated: 18 alerts incremented in dry-run after first cycle.
- **Evidence**: `staffops_ad_alert_fired_total{severity="critical"} 3`, `severity="warning"} 15`
- **Action**: closed (uncommitted). Part of P4.A.1.

## 2026-05-30 23:20 — ✅ RESOLVED: cycle_duration_seconds histogram bucket clip at 10s
- **Type**: resolved
- **Detail**: Replaced `prometheus.DefBuckets` (caps at 10s) with custom `[]float64{1, 2.5, 5, 10, 20, 30, 60}`. p99 is now visible above 10s — useful for detecting cycle stalls before they exceed the 30s interval.
- **Evidence**: New buckets visible at scrape; 3 cycles observed with le="10"=3 (no longer hitting +Inf only).
- **Action**: closed (uncommitted). Part of P4.A.1.

## 2026-05-30 23:20 — ⚠️ NOT YET RESOLVED: identity cardinality concern revised
- **Type**: working_as_designed (was tagged as concern)
- **Detail**: Original observability advisor flagged `identity` as cardinality bomb in metrics — audit confirmed this was overstated. Current Prom metrics already use only bounded labels (severity, signal, detector, kind). Real fix done was on AM alert side: the `workload` label sent to Alertmanager previously contained pod names; now uses bounded value via new `amWorkloadLabel` helper that calls `correlation.ExtractWorkload` for KindPod cases. Pod identity preserved in annotations, not labels. Prevents AM routing rule explosion.
- **Evidence**: dispatcher.go now has both `identityOf` (display, may be unbounded) and `amWorkloadLabel` (AM label, always bounded).
- **Action**: closed (uncommitted). Part of P4.A.2.

## 2026-05-30 23:20 — ✅ RESOLVED: multi-cluster constant labels missing
- **Type**: resolved (architectural improvement)
- **Detail**: Per `multicluster-label-strategy` steering, app should emit `cluster` constant label (kubernetes-mixin convention). Added via `prometheus.WrapRegistererWith({cluster: cfg.Cluster}, registry)` in both controller and worker main. Initial implementation also added `eks_cluster` constant label, but reverted after recognizing it's BDC-specific — moved to `scripts/observability/prometheus.yml` per-job `static_configs.labels` (correct architectural location). App stays generic; infra layer enriches.
- **Evidence**: `staffops_ad_controller_build_info{cluster="bdc-eks-prd",version="0.7.0"} 1` (app side, generic). Prom query side: `cluster=bdc-eks-prd, eks_cluster=local, environment=dev` (after scrape enrichment).
- **Action**: closed (uncommitted). Part of P4.A.3. README section "Multi-cluster labels" added explaining the SDK/Collector boundary for adopters.

## 2026-05-30 23:20 — replay-mode (P3.1) IN PROGRESS — paused at 5/16 tasks
- **Type**: progress note
- **Detail**: Started replay-mode implementation as pilot of spec-driven workflow (`.kiro/specs/replay-mode/`). Completed phases 1+2: window parser (T1, 22 sub-tests), VM range query (T2), Loki range query (T3), InMemStore baseline (T4, 7 tests), `baseline.Evaluator` interface refactor (T5, AdaptiveDetector now polymorphic). Paused to attack observability hardening blockers before continuing. Build clean, all tests green.
- **Evidence**: `controller/internal/replay/{window,inmem_baseline,*_test}.go` + extended `internal/ingestion/{metrics,logs}.go` with TimeSeries/QueryRange.
- **Action**: resume after observability hardening or deciding next direction. T6-T16 remaining (~5 days sequential).

## 2026-05-30 23:20 — code-review subagent created
- **Type**: tooling addition
- **Detail**: New subagent at `agents/subagents/code-review/` with rigorous quality-gate framework: 7 review gates (correctness, steering, idiomatic, readability, tests, performance, security), structured output (blockers/strong/suggestions/praise + coverage check), inter-agent collaboration table, behavior anti-patterns. Total subagent count now 10 (was 9). README + setup-agents.sh validated. Symlink installed at `~/.kiro/agents/code-review.json`.
- **Evidence**: `setup-agents.sh --validate` clean. `staffops.json` lists code-review in availableAgents + trustedAgents.
- **Action**: agent definition exists but **subagent tool spawning still broken in this env** (3 consecutive `No result` returns earlier today). Main agent can adopt the persona by reading `prompt.md` until subagent system is fixed.

## 2026-05-31 00:30 — ✅ RESOLVED: service-level anomalies collapse into "/" workload key
- **Type**: resolved
- **Detail**: `workloadKey()` em `correlator.go` usava `namespace + "/" + pod`. Service-level anomalies (latency_p99_by_service, error_rate_by_service) têm pod vazio → todas caíam no grupo "/". Fix: usar `service_name` como fallback quando pod está vazio; sentinel `_unknown_` quando ambos vazios. Agora cada service distinto agrupa por `namespace/service_name`.
- **Evidence**: era 348 anomalies no key "/" (29x o segundo lugar). Go tests pass.
- **Action**: closed. Commit `940316a`. Resolve os ALARMs "Service-level colapsam" e "306/348 anomalies no workload-key vazio".

## 2026-05-31 00:30 — ✅ RESOLVED: ML feature count mismatch (IsolationForest)
- **Type**: resolved
- **Detail**: `multivariate.py` usava `sorted(samples.keys())` → schema dinâmico. Pod-level dava 6+ features, service-level 3-5 → `ValueError: X has 3 features, but IsolationForest is expecting 6`. Fix: `CANONICAL_FEATURES` (10 features fixas) + `_normalize()` que padda ausentes com 0.0 e descarta keys desconhecidas. Modelo sempre treina/prediz com schema estável.
- **Evidence**: era ~33% error rate. Python test: pod-level (6) + service-level (3) na mesma instância sem crash.
- **Action**: closed. Commit `940316a`. Resolve o ALARM "ML feature count mismatch (BUG IMPORTANTE)" e desbloqueia confiabilidade do P2.1.

## 2026-05-31 00:35 — replay-mode (P3.1) progress — 12/16 tasks done
- **Type**: progress note
- **Detail**: Retomado replay-mode. T6-T12 implementados via fan-out (dev + code-review round-table): ReplayConfig (T6), tick simulator com warmup split + SIGINT partial flush + graceful query-error skip (T7), range-to-instant adapter SamplesAt (T8), Report struct + WriteJSON full schema (T9), WriteMarkdown com sparklines (T10), CLI flags --replay/--from/--to/--output/--warmup-fraction/--max-range/--max-anomalies + pre-flight checks (T11), exec metrics in-memory (T12). 2 code reviews aprovados (0 blockers). WriteMarkdown agora propaga write errors (paridade com WriteJSON).
- **Evidence**: 1275+ linhas, golden-file tests JSON+MD, all Go tests green. Commits `ee64d65`, `5ff1f8c`.
- **Action**: remaining T13 (integ test), T14 (smoke test — precisa stack rodando), T15 (README), T16 (ROADMAP move to Done). ~2 days.
