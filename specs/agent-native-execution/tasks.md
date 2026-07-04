# Tasks: Agent-Native Execution

> Ordem pensada para destravar o mais cedo possível: Phase A remove a fricção de
> execução (scripts + permissões), Phase B congela decisões, Phase C limpa o ruído
> que polui diffs de agente, Phase D fecha o loop de documentação.
> Convenções: builds via Docker, sem SDK local; conventional commits; nunca commitar
> sem aprovação explícita.

## Phase A: Camada de execução (`scripts/dev/` + permissões)

- [ ] A1: Criar `scripts/dev/lib.sh` — resolução de repo root, token (`gh auth token`
      → fallback `GITHUB_TOKEN`), helpers de docker run com GOPRIVATE/git-credential
      (extrai a prosa de AGENTS.md para código único e testado)
- [ ] A2: `scripts/dev/build-go.sh` — compila controller + worker (depends on: A1)
- [ ] A3: `scripts/dev/test-go.sh` — `go test ./...`; flag `--coverage` roda
      `-coverprofile` sobre `./internal/...` e imprime o total (depends on: A1)
- [ ] A4: `scripts/dev/lint.sh` — gofmt (diff, não write) + `go vet` + ruff no `ml/`
      (mesmas invocações do `test.yml`) (depends on: A1)
- [ ] A5: `scripts/dev/test-ml.sh` — `pip install -e '.[dev]' && pytest` em
      `python:3.11-slim` (depends on: A1)
- [ ] A6: `scripts/dev/verify.sh` — orquestra A2-A5 na ordem CI; saída resumida
      gate-a-gate; exit ≠ 0 no primeiro gate quebrado (depends on: A2-A5)
- [ ] A7: `scripts/dev/doctor.sh` — preflight: docker, gh auth, acesso ao módulo
      privado `staffops-otel-libs`, `.env` se stack local (depends on: A1)
- [ ] A8: `.claude/settings.json` (versionado) — allowlist: `scripts/dev/*`,
      `docker run/build/compose` (não `system prune`), git read-only, `gh` read-only,
      mkdocs. Deny explícito: `git push`, `docker system prune`. (depends on: A6)
- [ ] A9: Skill de projeto `verify` (`.claude/skills/verify/SKILL.md`) — aponta
      `scripts/dev/verify.sh`, explica gates e como ler falhas (depends on: A6)
- [ ] A10: Skill de projeto `replay` — como rodar replay/medição (build, flags,
      onde sai o report, o que provar/não provar) — prepara o terreno do P0.1
      (depends on: A2)

## Phase B: Decisões (ADRs)

- [ ] B1: Criar `docs/adr/` com template + migrar Decisions 1-8 de
      `docs/architecture/decisions.md` para `0001`-`0008` (corpo migra; decisions.md
      vira índice com links — sem duplicação)
- [ ] B2: ADR-0009 — **branch model trunk-based**: remover job `guard` e triggers
      `dev` de `test.yml`/`sast.yml`; registrar racional (histórico do repo já é
      trunk) (depends on: B1)
- [ ] B3: ADR-0010 — **freeze da escalada de severidade via ML** até P0.1: registrar
      o defeito (IF treinado só em amostras anômalas, `_history` ilimitado, sem
      persistência) e o critério de reversão (números do measurement gate)
      (depends on: B1)
- [ ] B4: Aplicar ADR-0009 nos workflows (`test.yml`, `sast.yml`: dropar `guard` +
      `dev` dos triggers) (depends on: B2)
- [ ] B5: Aplicar ADR-0010 no código — gate mínimo: não escalar `warning→critical`
      por `MLDetection` (manter anotações `ml_score`/`ml_contributors` para coleta
      de dados); default `ML_ENABLED` inalterado; teste cobrindo o não-escalonamento
      (depends on: B3)

## Phase C: Higiene que polui diffs de agente

- [ ] C1: `gofmt -w` nos 16 arquivos pendentes → dropar `continue-on-error` do
      `lint-go` (re-arma o gate) (depends on: A4)
- [ ] C2: Corrigir context leak em `internal/redis/client_test.go:25` (go vet)
      (depends on: C1)
- [ ] C3: Bump `google.golang.org/grpc` 1.67.1 → ≥1.79.3 (CVE-2026-33186 CRITICAL) +
      `go.opentelemetry.io/otel/sdk` (CVE-2026-24051); rodar verify completo
      (depends on: A6)
- [ ] C4: Resolver PH.25 — deduplicar pinning de deps do `ml/Dockerfile` vs
      `pyproject.toml` (uma fonte de verdade)

## Phase D: Documentação de entrada

- [ ] D1: Seção "Current focus" no AGENTS.md — 5-8 linhas: fase ativa
      (→ `specs/synthetic-injection/`), comando de verify, link pro ROADMAP;
      regra: atualizar a cada mudança de foco (definition of done de milestone)
      (depends on: A6)
- [ ] D2: Atualizar AGENTS.md — substituir os blocos docker de prosa por
      `scripts/dev/*` (prosa vira referência de "o que o script faz por dentro")
      (depends on: A6)
- [ ] D3: Atualizar ROADMAP — marcar decisão de branch model como resolvida
      (ADR-0009), referenciar este spec na seção CI/CD rollout debt
      (depends on: B4)
- [ ] D4: `scripts/README.md` — documentar `scripts/dev/` (depends on: A6)

## Fora deste spec (próximo passo após conclusão)

Executar **`specs/synthetic-injection/`** (P0.1) usando a infra criada aqui — é o
gate que decide todo o resto. Depois **`specs/competitive-teardown/`** (P0.2).
PRD de "eval harness como produto" só se escreve **depois** dos números de P0.1.

## Nota de ordenação

A1-A8 primeiro (destravam tudo). B5 e C3 são as duas únicas mudanças de código de
produção — pequenas e independentes. C1 idealmente num commit isolado (só
formatação) para não poluir history de mudanças reais.
