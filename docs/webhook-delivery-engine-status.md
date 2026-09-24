# Status: Webhook Delivery Engine

**Atualizado em:** 2026-09-24
**Branch:** `feature/sprint-1-vertical-slice`

## Planejamento

- ✅ PRD e user stories
- ✅ Security Review do PRD
- ✅ Design API-first
- ✅ Arquitetura, ADRs e AGENTS.md
- ✅ Security Review da arquitetura
- ✅ Arquitetura de dados
- ✅ Security Review do schema — aprovado condicionalmente após remediações
- ✅ Backlog do MVP

## Sprint 0 — Fundação reproduzível

- ✅ S0-01 — Inicializar módulo e estrutura
- ✅ S0-02 — Configuração tipada e startup/shutdown
- ✅ S0-03 — Logger e erros base
- ✅ S0-04 — PostgreSQL local e migrations
- ✅ S0-05 — CI e ferramentas pinadas
- ✅ Code Review — aprovado, sem blockers ou warnings
- ✅ QA — aprovado, 35/35 casos automatizados e matriz operacional real sem falhas

### Evidencias do Stack Agent

- `gofmt`, `go mod tidy -diff`, testes, race detector, vet, Staticcheck e `govulncheck` aprovados.
- Binarios `api`, `worker` e `chaoslab` compilados com Go 1.27.1.
- Compose validado; PostgreSQL 17.11 iniciou e a migration `000001_foundation.sql` subiu do zero.
- A role `wde_api` foi verificada sem privilegio de leitura sobre `wde.schema_metadata`.

### Correcoes do Code Review — ciclo 1

- Migrator separado em Dockerfile proprio; Compose e CI agora constroem todos os targets e executam smoke real.
- Peppers e keyrings produtivos possuem formato versionado, 32 bytes de material e validacao de separacao.
- Chaos Lab usa loopback como default fora do container.
- API e worker abrem `pgxpool`, validam conexao, role e schema; API separa liveness de readiness.
- Logger nao possui allowlist global mutavel e limita mensagens, strings, erros e `Stringer` a 256 bytes.
- Migration real validou `wde.schema_version()`, roles esperadas e ausencia de `SELECT` direto para API/worker.
- Build local completo das imagens nao foi repetido apos a separacao dos Dockerfiles por limite de disco; esse gate esta materializado no job `compose-smoke` da CI.

### Correcoes do Code Review — ciclo 2

- Probes removidas do listener publico da API e movidas para listener operacional em loopback.
- Worker recebeu listener operacional separado; API e worker verificam banco, role e schema em cada readiness.
- Compose publica API operacional em `127.0.0.1:9090` e worker operacional em `127.0.0.1:9091`.
- Teste real confirmou `404` nas probes publicas, liveness `200` e readiness `200`; apos parar o PostgreSQL, liveness permaneceu `200` e ambas as readiness retornaram `503`.

### QA da Sprint 0

**Veredicto:** ✅ Aprovado em 2026-09-23.

#### Qualidade estatica e testes automatizados

- `make check` passou apos os testes de QA: `gofmt`, `go mod tidy -diff`, `go test ./...`, `go test -race ./...`, `go vet ./...`, Staticcheck, `govulncheck`, Goose validate e build dos tres binarios.
- A suite executou 35 casos, incluindo subtestes, em sete pacotes testados: 35 passaram e zero falhou.
- `govulncheck` v1.8.0, com base atualizada em 2026-09-16, nao encontrou vulnerabilidades alcancaveis.
- Testes adicionais cobrem profiles `local`, `test` e `production`, configuracao produtiva completa e rejeicao da reutilizacao de peppers/keyrings.
- Cobertura das fronteiras principais: configuracao 83,8%, logging 83,8%, listener operacional 92,9% e problem details 75,0%. Nao existe meta global de cobertura para a fundacao.

#### Banco e migrations reais

- PostgreSQL 17.11 foi iniciado em ambiente descartavel isolado, usando a imagem `postgres:17` ja existente e porta `127.0.0.1:55432`.
- A migration `000001_foundation.sql` subiu do zero, reportou status aplicado, desceu e subiu novamente com schema version 1.
- Com a migration removida, API e worker falharam no startup; nenhum processo tentou migrar automaticamente.
- Roles `wde_owner`, `wde_migrator`, `wde_api`, `wde_worker` e `wde_admin` foram verificadas sem superuser, create role/database, replication ou bypass RLS; `wde_owner` e a unica `NOLOGIN`, enquanto migrator e roles de runtime/admin possuem login conforme o desenho.
- Database/schema pertencem a `wde_owner`; `PUBLIC` nao possui connect no database nem usage/create no schema `wde`.
- API e worker executam apenas `wde.schema_version()` e nao possuem `SELECT` direto em `wde.schema_metadata`; a funcao e `SECURITY DEFINER`, pertence a `wde_owner`, fixa `search_path=pg_catalog` e nao concede `PUBLIC EXECUTE`.
- Credencial incorreta foi rejeitada pelo PostgreSQL, e API/worker recusaram iniciar quando conectados com a role um do outro sem vazar detalhes no stderr.

#### Operacao e lifecycle

- API, worker e Chaos Lab iniciaram pelos binarios locais e encerraram por `SIGINT` dentro do timeout, registrando somente mensagens estruturadas allowlisted.
- Na API publica, `/healthz` e `/readyz` retornaram `404 application/problem+json` com request ID gerado pelo servidor; os listeners operacionais retornaram liveness/readiness `200` com banco disponivel.
- Com o PostgreSQL parado, liveness da API e do worker permaneceu `200`, readiness mudou para `503 not_ready`; apos reiniciar o banco, ambas retornaram automaticamente a `200`.
- Chaos Lab respondeu `200` no profile `test` e recusou startup no profile `production`.
- `docker compose --profile demo config --quiet` passou; portas publicadas usam loopback, PostgreSQL usa volume nomeado e o healthcheck nao contem credencial.
- Make targets e workflow CI cobrem formatacao, modulos, testes, race, vet, Staticcheck, `govulncheck`, migrations, tres binarios e smoke Compose; GitHub Actions usam SHA imutavel.

#### Restricao operacional desta execucao

- Imagens de aplicacao/migrator nao foram reconstruidas localmente para preservar os aproximadamente 5,8 GiB livres. Os Dockerfiles e o job `compose-smoke` foram inspecionados, o Compose foi renderizado/validado e os binarios foram exercitados contra PostgreSQL real. O ambiente descartavel de QA, incluindo seu volume, foi removido; a imagem `postgres:17` foi preservada.
- Sugestao nao bloqueadora herdada do Code Review: a tabela do README ainda mostra `:8081` para o Chaos Lab, embora o texto e o codigo usem corretamente `127.0.0.1:8081`.

## Próximo gate

Sprint 0 concluida, revisada, aprovada em QA e integrada a `main` no commit `63feb8f`.

## Sprint 1 — Primeiro corte vertical

- S1-01 — Schema mínimo tenant-safe — implementada
- S1-02 — Bootstrap local de workspace e API key — implementada
- S1-03 — Criar e consultar endpoint — implementada
- S1-04 — Publicar evento idempotente — implementada
- S1-05 — Worker de concorrência 1 — implementada
- S1-06 — HMAC v1 — implementada
- S1-07 — Chaos Lab e consulta de delivery — implementada
- ✅ Code Review — aprovado no ciclo 3, sem blockers ou warnings
- ✅ QA — aprovado, 71/71 resultados automatizados e fluxo E2E real sem falhas

O corte vertical foi validado em PostgreSQL 17 com migration `up/down/up`, roles reais, RLS/ACLs, bootstrap/revogação e E2E `endpoint → event → worker → Chaos Lab → succeeded`. A Sprint somente será marcada concluída depois de Code Review e QA independentes.

### Remediações do Code Review

- CI alinhada ao `schema_version=2` e com integração real usando roles PostgreSQL e E2E completo.
- Autenticação e RLS recusam workspaces `suspended`/`deleting`.
- Bootstrap serializado por advisory lock, com saída única segura, `fsync` e limpeza caso o commit falhe.
- Claim valida o snapshot completo antes de alterar a delivery ou criar attempt.
- `WDE_ALLOW_HTTP_DESTINATIONS` controla efetivamente cadastro e envio HTTP loopback.
- Contrato OpenAPI e testes negativos ampliados; reavaliação de Code Review e QA continua pendente.
- `current_workspace_id()` revoga `PUBLIC EXECUTE` explicitamente; teste real confirma execução somente por `wde_api`.
- API/CLI, endpoints, criptografia, configuração e entrega HTTP foram decompostos em unidades coesas; nenhum arquivo Go de produção excede 200 linhas.

### QA da Sprint 1

**Veredicto:** ✅ Aprovado em 2026-09-24.

- `make check` passou com testes, race detector, vet, Staticcheck, `govulncheck`, validação de migrations e build dos três binários.
- PostgreSQL 17.11 real passou por migration `up/down/up`, matriz de roles/ACLs, RLS fail-closed e proteção append-only.
- Bootstrap concorrente e pelo binário confirmou emissão única, arquivo `0600`, rollback seguro e revogação.
- Cem publicações concorrentes produziram exatamente um evento e uma delivery.
- O E2E validou create/get de endpoint, escopos, isolamento cross-tenant, 409, 413, worker, HMAC, Chaos Lab, estado `succeeded` e timeline sem material sensível.
- Relatório completo: `docs/webhook-delivery-engine-qa-sprint-1.md`.

## Próximo gate

Sprint 1 concluída, revisada e aprovada em QA. Próximo passo: integrar a branch em `main` e iniciar a Sprint 2 — Confiabilidade e concorrência.
