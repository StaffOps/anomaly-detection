# Experiment: Competitive Teardown (P0.2)

> **Isto é uma spec de experimento, não de feature.** O entregável não é código de
> produto — é uma **decisão com evidência**: existe um produto a construir, ou o valor
> sobrevivente cabe em config sobre ferramentas que já existem?

## Visão geral

Depois de quatro rounds de revisão, a conclusão provada é que o **detector** é
commodity (`docs/architecture/decisions.md` Decision 8). A hipótese **não-provada** é
que existe um produto na camada de **originação causal de incidente**
(`docs/hypothesis-causal-origination.md`).

Este experimento testa a hipótese pelo **caminho da invalidação**: pega cada pedaço de
valor que o sistema atual entrega e **tenta reproduzi-lo** em duas ferramentas
incumbentes — `predict_linear`/recording rules no VictoriaMetrics (que já temos) e um
playbook do Robusta. 

A lógica é binária e desconfortável de propósito:
- **Se porta barato** → era config, não produto. O resultado honesto é *não construir*
  — shipar as rules + o playbook e parar.
- **Se resiste** → o pedaço que não cabe num playbook/rule é o **core empírico** do
  produto. Achado por experimento, não por argumento.

> **Por que isto vem antes de construir qualquer coisa**: é barato (dias, não semanas),
> e pode salvar meses construindo um produto que é, na verdade, um arquivo de
> configuração. O custo de rodar o experimento é trivial perto do custo de errar a
> aposta.

## O que NÃO é este experimento

- Não é construir o produto causal. É testar se ele precisa existir.
- Não é um benchmark de performance. É um teste de **reprodutibilidade do valor**.
- Não é avaliar o detector (isso é P0.1). Aqui o detector já é assumido commodity.

## Inventário de valor a testar

Cada capacidade que o sistema entrega hoje (ou pretende entregar) vira uma **hipótese
de portabilidade** falsificável. Para cada uma: tenta reproduzir; registra se portou,
com que esforço, e o que se perdeu.

| # | Capacidade do sistema | Tenta reproduzir como | Hipótese (H0 = "é commodity, porta") |
|---|----------------------|----------------------|--------------------------------------|
| C1 | Saturação rumo a teto (`queue_depth`, `hikaricp_pending`, `heap_growth`) | `predict_linear(metric[30m], 3600) > capacity` no `vmrules.yaml` existente | H0: porta como recording/alert rule de 1 linha |
| C2 | Workload-collapse horizontal (≥3 sibling pods → 1 alerta) | Alertmanager `group_by: [workload]` | H0: porta como config de routing |
| C3 | Enriquecimento de alerta (cpu/mem/error/latency ratios + contexto) | Robusta playbook (k8s-aware enrichment) | H0: porta como playbook |
| C4 | Deep-links (Grafana/Tempo/Loki/runbook) | Robusta playbook / annotation templates | H0: porta como template |
| C5 | Dedup / dispatch | Alertmanager (já é o backend de dispatch) | H0: já é Alertmanager, trivial |
| C6 | **Cadeia causal intra-runtime** (.NET threadpool→queue→latency→errors; Go goroutine-leak→OOM; Python loop-block→pileup) | Robusta playbook? recording rules encadeadas? | **H1 (a aposta): NÃO porta** — playbooks de topologia operam na aresta entre serviços, não na causalidade intra-processo ordenada |

C1-C5 são esperados portar (confirmam "commodity"). **C6 é o único candidato a
resistir** — e se resistir, é o produto.

## Critério de decisão (binário, definido ANTES de rodar)

Para evitar racionalização pós-hoc, os critérios são fixados agora:

### Uma capacidade "PORTA" se:
- É reproduzível em ≤ ~1 dia de trabalho de config, E
- Não perde nenhuma propriedade essencial (o operador recebe o mesmo valor), E
- Não exige código customizado além de templates/queries declarativos.

### Uma capacidade "RESISTE" se:
- Reproduzi-la exige codificar **lógica causal** (ordem, precedência, "X precede Y"),
  não apenas agregação/threshold/template, E
- Nenhum playbook/rule incumbente expressa essa lógica sem virar, na prática,
  reimplementar o que estamos avaliando.

### Decisão final do experimento:
- **Se só C1-C5 portam e C6 resiste** → o produto é a camada causal intra-runtime.
  Promove a hipótese a ADR. Escopo do produto = C6, e nada mais (C1-C5 viram config
  shipável).
- **Se C6 também porta** (cabe num playbook honesto) → **não há produto**. Shipar
  rules + playbook, fechar a hipótese como refutada, e o "sistema" vira um conjunto de
  configs + um shim fino. Resultado válido e bom — evita construir o que já existe.
- **Se C6 é ambíguo** (porta parcialmente) → nomear exatamente a fração que resiste;
  essa fração é o produto mínimo.

## Tarefas do experimento

### Phase 1: Reproduzir o commodity (esperado portar)
- [ ] T1: Escrever as `predict_linear` rules pra C1 (queue_depth, hikaricp_pending, heap_growth) no formato do `vmrules.yaml` existente. Registrar esforço.
- [ ] T2: Configurar Alertmanager `group_by: [workload]` reproduzindo C2. Confirmar que colapsa o mesmo que o workload-collapse atual.
- [ ] T3: Montar um Robusta playbook reproduzindo C3+C4 (enrichment + deep-links) sobre um alerta de exemplo. Registrar o que portou e o que se perdeu.
- [ ] T4: Confirmar C5 (dispatch já é Alertmanager — trivial, documentar).

### Phase 2: Atacar o core candidato (o teste de verdade)
- [ ] T5: Pegar UMA cadeia causal do `degradation-model.md` (sugestão: .NET N1, threadpool→queue→latency→errors, a mais coberta por métricas).
- [ ] T6: Tentar expressá-la como Robusta playbook — o playbook consegue afirmar "a fila encheu ANTES da latência subir, logo a causa é threadpool, não dependência"?
- [ ] T7: Tentar expressá-la como recording rules encadeadas no VM — dá pra codificar a *ordem temporal* (precedência) que distingue N1 de N3 sem reimplementar correlação?
- [ ] T8: Registrar o resultado de T6/T7 contra os critérios "PORTA/RESISTE" fixados acima.

### Phase 3: Decisão
- [ ] T9: Consolidar a matriz C1-C6 com veredito por capacidade (portou / resistiu / parcial) + evidência (o artefato de config tentado).
- [ ] T10: Tomar a decisão do experimento (produto / não-produto / produto-mínimo) conforme os critérios.
- [ ] T11: Atualizar `docs/hypothesis-causal-origination.md` — gate 2 com o veredito + evidência.
- [ ] T12: Se "produto": atualizar `docs/architecture/decisions.md` promovendo a hipótese a ADR, com escopo = a fração que resistiu. Se "não-produto": fechar a hipótese como refutada e registrar as configs shipáveis como o entregável real.
- [ ] T13: Marcar P0.2 no ROADMAP com o resultado.

## Dependências e precondições

- **Robusta** disponível pra teste (ou um ambiente onde dê pra instalar e configurar um
  playbook de exemplo). Se não houver, T3/T6 viram análise documental do que um
  playbook Robusta *poderia* expressar — mais fraco, mas ainda informativo. **Decisão
  em aberto: temos Robusta disponível, ou é análise documental?** (delegar a `gitops`
  pra confirmar).
- `degradation-model.md` escrito (✅ já existe) — fonte da cadeia causal de T5.
- Idealmente roda **depois ou em paralelo a P0.1** — o número do P0.1 informa se o
  detector commodity vale até ser reproduzido (se o recall é péssimo, nem C1-C5
  importam muito).

## Ordem relativa a P0.1

P0.1 (medição) e P0.2 (teardown) são os dois gates da hipótese. Recomendação:
**P0.1 primeiro ou em paralelo** — o número do detector é insumo. Mas P0.2 não *bloqueia*
em P0.1: dá pra rodar o teardown de C1-C5 e da cadeia causal independentemente do número
de recall. Os dois juntos é que decidem a hipótese.

## Riscos do experimento

| Risco | Mitigação |
|-------|-----------|
| Racionalização pós-hoc ("C6 resiste porque eu quero que resista") | Critérios PORTA/RESISTE fixados ANTES de rodar (acima) |
| Robusta indisponível → teardown vira teoria | Declarar explicitamente quando for análise documental vs teste real; análise documental é veredito mais fraco |
| Esforço de portar subestimado/superestimado | Registrar esforço real (horas) por capacidade, não impressão |
| Viés de quem construiu o sistema | Idealmente delegar T6/T7 a `gitops`/`sre` (quem conhece Robusta/VM rules) em vez do autor do sistema |
