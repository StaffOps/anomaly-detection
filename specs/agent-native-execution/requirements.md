# Feature: Agent-Native Execution (repo executável por agentes sem fricção)

## Visão geral

Tornar o repositório **executável de ponta a ponta por agentes de código** (Claude
Code e equivalentes) sem intervenção humana além de aprovação de commits. Hoje o
repo tem documentação excelente (AGENTS.md, ROADMAP, specs com tasks e dependências),
mas a **camada de execução** tem fricção: comandos de build/test vivem como prosa
multi-linha, não há permissões de projeto versionadas, não há verificação de
um comando, e um agente iniciando frio não tem ponto de entrada inequívoco.

Este spec NÃO substitui a Phase 0 do ROADMAP — ele a **destrava**: o objetivo é que
qualquer agente consiga pegar `specs/synthetic-injection/tasks.md` e executar as 29
tasks sem tropeçar em tooling.

## Diagnóstico (2026-07-04)

| Lacuna | Evidência | Impacto no agente |
|--------|-----------|-------------------|
| Sem permissões de projeto | `.claude/` contém só `settings.local.json` com 2 entradas hiper-específicas | Prompt de permissão a cada `docker run`; execução autônoma impossível |
| Comandos como prosa | Blocos docker de 6+ linhas em AGENTS.md, dependentes de `TOKEN=$(gh auth token)` | Cópia frágil, erros silenciosos de quoting, cada agente reinventa |
| Sem verify de 1 comando | Não existe `scripts/dev/`; CI é a única "verdade" dos gates | Agente não consegue validar mudança localmente igual à CI |
| Sem ponto de entrada | Prioridade atual só se deduz lendo ROADMAP (560 linhas) + análise | Agente frio gasta contexto redescobrindo o que já foi decidido |
| ADRs informais | `docs/architecture/decisions.md` é um log único; 2 decisões novas (branch model, ML freeze) sem registro | Decisões re-litigadas a cada sessão |
| Ruído de formatação | 16 arquivos fora do gofmt (dívida CI conhecida) | Todo diff de agente vem poluído com reformatação acidental |
| Ambiente não-validável | Sem doctor/preflight; falhas de `gh auth`/docker/env aparecem no meio da execução | Runs longos que falham tarde por pré-requisito ausente |

## User Stories

### US-1: Verificação de um comando

WHEN um agente conclui uma mudança em `controller/` ou `ml/`
THEN ele SHALL poder rodar **um comando** (`scripts/dev/verify.sh`) que executa
localmente os mesmos gates da CI (fmt, vet, testes Go, testes ML, cobertura)
AND o script SHALL falhar com mensagem acionável indicando qual gate quebrou.

### US-2: Execução sem prompts de permissão

WHEN um agente executa o fluxo padrão (build/test via Docker, git read-only,
`gh` read-only, scripts de `scripts/dev/`)
THEN as permissões versionadas em `.claude/settings.json` SHALL cobrir essas
operações sem prompt
AND operações destrutivas (push, docker system prune, rm fora do repo) SHALL
continuar exigindo aprovação.

### US-3: Cold start em uma leitura

WHEN um agente inicia uma sessão nova
THEN AGENTS.md SHALL conter uma seção "Current focus" curta apontando a spec/fase
ativa e os comandos de verificação
AND essa seção SHALL ser atualizada quando o foco mudar (parte da definition of done
de cada milestone).

### US-4: Decisões estáveis entre sessões

WHEN uma decisão de arquitetura/processo é tomada
THEN ela SHALL virar um ADR numerado em `docs/adr/`
AND `docs/architecture/decisions.md` SHALL permanecer como índice (sem duplicar corpo)
AND as duas decisões pendentes já aprovadas SHALL ser registradas:
  1. **Branch model: trunk-based** (dropa `guard`/`dev` dos workflows)
  2. **Freeze da escalada de severidade via ML** até o gate P0.1 produzir números
     (o Isolation Forest atual treina só sobre amostras já anômalas — a
     "confirmação" não tem base estatística hoje)

### US-5: Preflight de ambiente

WHEN um agente (ou humano) vai iniciar trabalho
THEN `scripts/dev/doctor.sh` SHALL validar: docker disponível, `gh auth token`
resolve, versões esperadas, e (se stack local for necessária) variáveis de
`.env` presentes
AND SHALL sair com código ≠ 0 e instrução de correção por item faltante.

## Requisitos não-funcionais

- **Idempotência**: todos os scripts de `scripts/dev/` rodáveis N vezes sem efeito
  colateral acumulado.
- **Paridade com CI**: `verify.sh` executa exatamente os gates de `test.yml`
  (mesmas flags), para que "verde local" preveja "verde CI".
- **Sem SDK local**: mantém a convenção — tudo via Docker (`golang:1.22-alpine`,
  `python:3.11-slim`).
- **Org-neutro**: nenhum endpoint/org hardcoded nos scripts; token via
  `gh auth token` ou `GITHUB_TOKEN`/`DOCS_DEPLOY_TOKEN`.

## Fora de escopo

- Qualquer mudança no detector, correlator ou ML além do freeze de escalada (US-4.2).
- Novas specs de produto (bloqueadas até P0.1/P0.2 — ver ROADMAP Phase 0).
- Hooks de automação complexos (avaliar depois que o fluxo básico rodar liso).
