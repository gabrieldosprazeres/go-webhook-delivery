# Status: Webhook Delivery Engine

**Atualizado em:** 2026-09-25
**Branch:** `feature/easypanel-production-demo`

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

## Sprint 2 — Confiabilidade e concorrência

- ✅ S2-01 — Claim em batch com fairness — implementada
- ✅ S2-02 — Lease, fencing e recuperação — implementada
- ✅ S2-03 — Retry, jitter e DLQ — implementada
- ✅ S2-04 — Graceful shutdown — implementada
- 🔄 Code Review — ciclo 2 corrigido; revalidação pendente
- ✅ QA — aprovado, 121/121 casos e subcasos na regressão real, 17/17 cenários de integração e stresses sem falhas

### Evidências do Stack Agent

- Migrations `000003`–`000006` aplicadas do zero em PostgreSQL 17; a cadeia física chegou à versão Goose 6 e somente a última etapa elevou o schema lógico para v3. `down-to 2/up` também foi validado.
- Cada migration possui menos de 200 linhas e cada função privilegiada menos de 100; schema, transição, entrypoint e finalização ficaram separados para auditoria sem quebrar a atomicidade do claim.
- Os boundaries Goose 3/4/5 preservam integralmente contratos e grants v2; somente a `000006` troca claim/finalize e publica schema v3 atomicamente. O caminho de downgrade restaura v2 e a versão lógica na mesma transação antes de remover helpers.
- Teste PostgreSQL dedicado percorre `up-to 3/4/5/6` e `down-to 5/4/3/2`, validando versões lógica/física, existência e `EXECUTE` de contratos v2/v3, isolamento das funções de staging e readiness incompatível fail-closed.
- Claim em batch usa `FOR UPDATE SKIP LOCKED`, limita o pedido aos slots livres e mantém cursor monotônico persistido por workspace/endpoint via sequence, sem row lock global, com helpers privilegiados internos e ACL mínima.
- Deadline tipado de claim tem default de 200 ms, teto de 2 s e nunca pode exceder poll ou lease; uma transação segurando workspace/endpoint/delivery do tenant A não impediu o tenant B de progredir em menos de 500 ms.
- Testes multi-ciclo com batch unitário, workers concorrentes e endpoint na capacidade provaram alternância persistente, ausência de claim duplicado e progresso do tenant/endpoint saudável.
- Recuperação real aguardou um lease de 20 segundos expirar, abandonou o attempt anterior, incrementou o fencing token e retomou em menos de 5 segundos após a expiração.
- Finalização com owner/token obsoleto afetou zero linhas; o detentor atual finalizou e o histórico manteve `abandoned/stale` seguido de `completed/success`.
- Dez falhas transitórias e três crashes sucessivos produziram exatamente o máximo de attempts, estado `dead_letter`, histórico íntegro e nenhum `max+1`.
- Classificação cobre rede/timeout, `408`, `425`, `429`, `5xx`, redirects, demais `4xx` e status hostil `600..999`; `Retry-After` inválido/acima do teto cai no full jitter determinístico.
- Polling vazio usa backoff exponencial com jitter injetável. Scheduler para novos claims ao cancelar, captura pânicos e respeita deadline absoluto mesmo com claim/job não cooperativo; subprocesso com job ativo validou `SIGTERM`.
- `EXPLAIN (ANALYZE, BUFFERS)` com 5.000 jobs confirmou `deliveries_ready_idx` sem scan sequencial; carga prolongada de 10.000 jobs manteve goroutines e memória dentro dos limites do teste.
- `make integration` passou em PostgreSQL 17 real; stress dos cenários de concorrência passou em 20 repetições e o cenário de slot unitário em 80 processos isolados.
- `make check`, integração com roles reais, `docker compose config` e `git diff --check` passaram; o PostgreSQL descartável em `tmpfs` foi removido após os testes.

### QA da Sprint 2

**Veredicto:** ✅ Aprovado em 2026-09-24.

- `make check` passou com testes, race detector, vet, Staticcheck, `govulncheck`, Goose validate e build dos três binários.
- A regressão completa sem cache e com PostgreSQL 17.11 real executou 121/121 casos e subcasos em 16 pacotes, sem skip ou falha.
- `make integration` passou em 17/17 cenários sobre banco já reutilizado, incluindo migrations, ACL/RLS, E2E legado e `SIGTERM`.
- Os cinco cenários críticos de concorrência passaram em 100/100 execuções; fairness com slot unitário passou em 80/80 processos isolados.
- O ciclo de migration agora prova `up-to 3/4/5/6`, `down-to 5/4/3/2` e re-upgrade `up-to 6`, mantendo o runtime v3 fail-closed nos boundaries incompatíveis.
- O harness de bootstrap foi isolado em database temporário próprio, eliminando dependência de banco global vazio; nenhum database temporário ficou remanescente.
- Status HTTP hostis passaram nos limites 600, 699 e 999; a integração confirmou sanitização e progresso do tenant seguinte.
- Relatório completo: `docs/webhook-delivery-engine-qa-sprint-2.md`.

## Próximo gate

Sprint 2 integrada a `main` nos commits `ca1b7f1` e `a6afea3`.

## Sprint 3 — Tenancy, replay e operação

- ✅ S3-01 — RLS e funções privilegiadas completas — implementada
- ✅ S3-02 — Quotas e limites persistentes — implementada
- ✅ S3-03 — Replay por geração — implementada
- ✅ S3-04 — Auditoria append-only — implementada
- ❌ Code Review — rodada 1 reprovada com 2 blockers e 2 warnings
- ✅ Correções da rodada 1 — implementadas e validadas pelo Stack Agent
- ✅ Re-review — rodada 2 aprovada, com zero blockers e zero warnings
- ✅ QA — aprovado, 147/147 testes e subtestes na regressão integral, stress S3 160/160 e zero skip

### Evidências do Stack Agent

- Migrations físicas `000007`–`000010` mantêm o schema lógico v3 e funções sem grants runtime até a publicação atômica de v4; o caminho `down` restaura v3 antes de remover staging.
- `audit_events`, `replay_commands` e `rate_limit_buckets` usam `ENABLE/FORCE RLS`, policies explícitas para executores `NOLOGIN` e nenhum DML direto de auditoria para API, worker ou admin.
- Funções privilegiadas possuem owner executor mínimo — incluindo roles NOLOGIN dedicadas `wde_quota_executor` e `wde_replay_executor` —, `SECURITY DEFINER`, `search_path=pg_catalog`, argumentos limitados, nomes totalmente qualificados, zero SQL dinâmico e `PUBLIC EXECUTE` revogado.
- O helper `tenanttx.Within` aplica `set_config(..., true)` na mesma conexão/transação e efetua rollback antes de devolver a conexão ao pool inclusive em cancelamento e panic.
- Teste com pool de uma conexão comprovou zero vazamento de workspace após commit, rollback e panic, além de isolamento cross-tenant.
- Quotas configuráveis consomem dimensões global/workspace/API key e delivery em uma transação; usam `transaction_timestamp()` estável, buckets expirados, HMAC com pepper exclusivo e nenhuma label de alta cardinalidade.
- Integração comprovou limite persistente após recriar o limiter, rollback atômico das dimensões, expiração de janela, fan-out máximo 100 sem persistência parcial e paginação 50/100 por cursor opaco.
- Replay exige escopo, motivo e chave de idempotência; advisory lock tenant-scoped serializa a mesma chave antes do recheck. Duas solicitações idênticas concorrentes geraram um comando/run/audit; conteúdo divergente retornou conflito de domínio sem `500`.
- O contrato HTTP real confirmou `deliveries:retry`, ausência de enumeração cross-tenant, `202` idempotente para repetição idêntica e `409` para a mesma chave com conteúdo diferente.
- Novo run preserva `attempt_sequence` monotônico e attempts anteriores: a integração observou `(run 1, attempt 1, sequence 1)` seguido de `(run 2, attempt 1, sequence 2)`.
- Payload expurgado recusou replay sem comando, auditoria ou transição parcial. Ações aceitas confirmam comando, transição e auditoria juntas.
- Bootstrap/revogação, criação de endpoint e replay registram snapshots append-only sem token, segredo, payload, URL completa, headers ou corpos; snapshots não possuem FK para entidades expurgáveis.
- O contrato OpenAPI inclui listagem paginada, replay idempotente, erros 409/429 e `Retry-After`; README, `.env.example`, Compose, Makefile e CI foram alinhados ao schema lógico v4.
- Após as correções, `make check` passou com testes unitários, race detector, `vet`, Staticcheck, `govulncheck`, lint OpenAPI, validação Goose e build dos três binários. `make integration` passou no PostgreSQL 17 com bootstrap, 15 cenários de regressão S1/S2, 8 cenários S3, pipeline real de produção e shutdown por sinal.

### Correções após Code Review — rodada 1

- O HMAC de quota v2 inclui tipo, operação, janela, workspace, API key e recurso aplicáveis; o `ON CONFLICT` também exige igualdade dos metadados. Teste pelo `newPublicServer` comprovou buckets distintos para atacante e vítima usando o mesmo UUID de delivery e ausência de quota poisoning.
- O pipeline real agora é `edge bounded/semaphore → authenticate → quota PostgreSQL → authorize scope → handler`. Token inválido foi limitado antes de novo lookup (`401 → 429`) e uma chave sem escopo consumiu quota autenticada antes do `403` (`403 → 429`). `X-Forwarded-For` não altera a origem observada.
- O edge limiter usa HMAC, fixed window, semáforo não bloqueante e LRU com cardinalidade máxima para dimensões global/origem/prefixo; teste de overflow concorrente confirma rejeição imediata.
- Cursores são binários v1, tenant-bound e HMAC-authenticated com pepper próprio. Cursor de outro workspace e cursor adulterado retornaram `400 invalid_page` na rota de produção.
- `WDE_CURSOR_PEPPER_FILE` é obrigatório na API produtiva e validado como distinto de auth, idempotency, fingerprint e rate limit.
- Respostas `409` e `429` documentam `application/problem+json`; o contrato é carregado, tem referências resolvidas e é validado semanticamente por `kin-openapi v0.149.0` pinado no módulo e no CI.
- Contenção concorrente do bucket global permitiu exatamente 8 de 32 solicitações em cinco execuções consecutivas. Replays concorrentes passaram 20 execuções e o pipeline produtivo completo passou 10 execuções consecutivas.

### QA da Sprint 3

**Veredicto:** ✅ Aprovado em 2026-09-24.

- `make check` passou com testes, race detector, `vet`, Staticcheck, `govulncheck`, validação OpenAPI/Goose e build dos três binários.
- A regressão integral sem cache em PostgreSQL 17.11 executou 147/147 testes e subtestes, em 18 pacotes testados, com zero skip e zero falha.
- `make integration` passou com bootstrap, 15 regressões S1/S2, oito cenários S3, pipeline HTTP real e shutdown por sinal.
- Os oito cenários S3 passaram em 160/160 execuções no mesmo processo; o pipeline público passou 20/20 execuções e 80/80 subcenários.
- Migrations foram verificadas do zero, em cinco ciclos de boundaries `up/down/up` e com schema lógico v4 publicado somente no boundary físico 10.
- Consultas independentes confirmaram owners executores mínimos, `SECURITY DEFINER`, `search_path=pg_catalog`, ausência de `PUBLIC EXECUTE`, RLS forçada e auditoria append-only sem canários.
- O QA corrigiu somente isolamento de fixtures: fechamento de pools não usados, database próprio para o plano de claim e suspensão de workspaces anteriores no E2E legado. Nenhum código de produção foi alterado.
- Relatório completo: `docs/webhook-delivery-engine-qa-sprint-3.md`.

## Próximo gate

Sprint 3 apta para commit e integração em `main`; em seguida, iniciar a Sprint 4 — Segurança de saída e dados.

## Sprint 4 — Segurança de saída e dados

- ✅ S4-01 — Cliente HTTP anti-SSRF — implementada
- ✅ S4-02 — Envelope AES-256-GCM — implementada
- ✅ S4-03 — Rotação HMAC — implementada
- ✅ S4-04 — Retenção, purge e restore quarantine — implementada
- ✅ Code Review — rodada final aprovada, com zero blockers e zero warnings
- ✅ QA independente — aprovado, 180/180 testes e subtestes na regressão real, stress S4 120/120 e zero skip

### Evidências do Stack Agent

- `internal/outboundhttp` aplica parser/IDNA estritos, resolve todos A/AAAA, bloqueia IPv4/IPv6/mapped e faixas especiais, fixa o IP validado no dial, mantém hostname/SNI, ignora proxy ambiental e recusa redirects.
- O transporte limita DNS, conexão, handshake TLS, response headers, tentativa e corpo; testes adversariais cobrem tabela IANA IPv4/IPv6, NAT64/6to4, DNS misto, rebinding, SNI com IP pinado, proxy, redirect, TLS inválido, lentidão e excesso de headers/corpo.
- Novas escritas usam envelope AES-256-GCM v2 com AAD tipado por workspace/recurso/metadados; v1 permanece leitura legada conhecida. Nonce fresco, tamper, transplante entre keyrings e versão desconhecida são testados fail-closed.
- Rotação concorrente/idempotente mantém uma chave `active` e uma `retiring`; o claim captura as duas e a tentativa emite assinatura dupla. Overlap é limitado a 1 hora–7 dias e segredo expirado é zerado all-or-none sem apagar o comando idempotente; retries posteriores retornam expiração/conflito sem mutação.
- Purge de payload bloqueia o evento/deliveries, abandona attempt iniciado, incrementa fencing, terminaliza com `payload_expired`, limpa o envelope e faz replay falhar fechado. Retenção também remove metadata, comandos, auditoria e buckets; batches rejeitam `NULL`/fora de 1–1.000 e o runner drena por progresso antes de um único scan final de backlog, publicando idade zero quando vazio. Purge de workspace serializa com rotação, avança por checkpoint/batch com lease/fencing, rebobina diante de dependência tardia e preserva tombstone/auditoria.
- Restore quarantine revoga todas as API keys e HMAC secrets, suspende workspace/endpoint, invalida leases e derruba readiness. Reconciliação só libera readiness quando nenhuma superfície restaurada está ativa.
- Roles `wde_rotation_executor` e `wde_maintenance_executor` são `NOLOGIN`; funções `SECURITY DEFINER` fixam `search_path=pg_catalog`, não expõem `PUBLIC EXECUTE` e roles login recebem somente os entrypoints necessários.
- Migrations físicas `000011`–`000019` têm menos de 200 linhas. O schema lógico v5 só é publicado em `000019`; boundary tests percorrem subida 3→19, descida 18→2 e re-upgrade fail-closed.
- O ciclo incremental `up → down-to 10 → up` passou em PostgreSQL 17 descartável, confirmando reversibilidade no boundary vazio e republicação segura do schema lógico v5.
- `make integration` passou integralmente em PostgreSQL 17 com regressões S1–S3 e doze cenários S4, incluindo classificação IANA/NAT64, rejeição de lotes nulos e teto físico de 1.000, retry tardio de rotação após purge, backlog com 2.501 registros, pais ainda inelegíveis, purge de workspace concorrente/checkpointado, ACL mínima e restore seguido de reconciliação.
- Os cenários adversariais S4 passaram três vezes consecutivas; a suíte anti-SSRF passou vinte vezes consecutivas, incluindo SNI com hostname original e IP pinado, header acima de 32 KiB e deadline compartilhado em múltiplos A/AAAA.
- `make check`, `docker compose --profile demo config --quiet` e `git diff --check` passaram; testes unitários, race detector, `vet`, Staticcheck, `govulncheck`, OpenAPI, Goose validate e build dos três binários estão verdes.

### QA da Sprint 4

**Veredicto:** ✅ Aprovado em 2026-09-24.

- `make check` passou após os testes de QA com unitários, integração, race detector, `vet`, Staticcheck, `govulncheck`, OpenAPI, Goose validate e build dos três binários.
- A regressão integral sem cache em PostgreSQL 17.11 executou 180/180 testes e subtestes em 20 pacotes, com zero skip e zero falha; `make integration` preservou as regressões S1–S3 e passou os doze cenários S4.
- A matriz S4 passou 120/120 em stress; restore, purge/rotation e rotação concorrente passaram 60/60; os canários de auditoria passaram 40/40 sob race detector.
- Anti-SSRF foi validado contra faixas IANA IPv4/IPv6, mapped/NAT64/transições, IDN, DNS misto/rebinding, IP pinning/SNI, proxy/redirect/TLS e limites de tempo/header/body.
- AEAD v2, AAD, keyrings, tamper/transplante, all-or-none e ausência de sinks passaram; leitura v1 de target, payload e segredos active/retiring passou 20/20 sob race.
- Rotação HMAC, overlap, assinatura dupla, expiração/purge, retries tardios e auditoria permaneceram íntegros sob concorrência.
- Retenção cobriu todas as categorias, batches/checkpoint, backlog/oldest, lease/fencing, cancelamento, dependência tardia, tombstone, restore snapshot, quarantine/readiness e reconcile.
- Migrations `000001`–`000019`, boundaries `up/down/up`, ACL/RLS, owners mínimos, `search_path`, ausência de `PUBLIC EXECUTE` e zero databases temporários foram confirmados em PostgreSQL real.
- Probes reais dos binários confirmaram listener público sem health, liveness/readiness separados, `503` durante quarantine, retorno a `200` após reconcile e shutdown limpo.
- O QA alterou somente harness/testes: fechamento de pools auxiliares, canários de auditoria e compatibilidade AEAD v1 explícita. Nenhum código de produção foi modificado.
- Relatório completo: `docs/webhook-delivery-engine-qa-sprint-4.md`.

## Próximo gate

Sprint 4 aprovada em Code Review e QA, apta para commit e integração em `main`; em seguida, iniciar a Sprint 5 — Observabilidade e operação.

## Sprint 5 — Observabilidade e demonstração

- ✅ S5-01 — Métricas Prometheus e traces OpenTelemetry — implementada pelo Stack Agent
- ✅ S5-02 — Probes e superfície operacional — implementada pelo Stack Agent
- ✅ S5-03 — Chaos Lab completo — implementada pelo Stack Agent
- ✅ S5-04 — OpenAPI e quickstart — implementada pelo Stack Agent
- ✅ Code Review independente — rodada 2 aprovada, com zero blockers e zero warnings
- ✅ QA independente — aprovado, 224/224 testes e subtestes na regressão real, zero skip e quickstart em 43,28 s

### Evidências do Stack Agent

- API e worker possuem registry Prometheus por processo com dimensões fechadas para HTTP, ingestão, tentativa, duração, retry, DLQ, fencing, fila, workers, inflight e retenção. Workspace, endpoint, evento, delivery, IP e URL nunca são labels.
- OpenTelemetry usa provider local, no-op sem endpoint, OTLP/HTTP opcional, sampling configurável e batch/flush bounded. Exportador indisponível não afeta o negócio; produção exige HTTPS. Propagação envia somente `traceparent`, descartando baggage/tracestate não confiáveis.
- Request spans recebem IDs opacos de evento/delivery e attempts possuem spans correlacionáveis; resolução/conexão de saída geram fases sem IP/hostname/URL/error bruto. Testes in-memory e scrapes usam canários de payload, API key, HMAC, query, header, resposta e auditoria.
- Os listeners operacionais separados expõem apenas `/livez`, `/readyz` e `/metrics`. Readiness valida role/schema lógico v5, restore quarantine, keyrings e, no worker, scheduler ativo e retenção. Imagens distroless usam o próprio binário para healthcheck loopback; nenhum `pprof` é registrado em produção.
- A migration física `000020` adiciona somente o agregado worker-only `delivery_queue_metrics()`, com executor `NOLOGIN`, `SECURITY DEFINER`, `search_path=pg_catalog`, timeout e grants mínimos; schema lógico permanece v5.
- Chaos Lab oferece success, fail-N, timeout, 429/Retry-After, falha permanente e verificação HMAC inclusive dual-key; estado expõe apenas contadores/status e nunca payload ou segredo.
- `make quickstart` cria ambiente PostgreSQL 17 efêmero, credencial sintética `0600` e demonstra assinatura, rotação, replay, retry, DLQ e observabilidade, com cleanup automático. O consumidor em `examples/hmac-consumer` valida corpo bruto em tempo constante e janela de cinco minutos.
- O quickstart E2E passou integralmente em PostgreSQL 17 real. `make integration` também passou após as mudanças finais, cobrindo bootstrap, regressões S1–S4, fila agregada worker-only, pipeline HTTP e shutdown por sinal.
- `make check` final passou com módulos organizados, unitários, integração, race detector, `vet`, Staticcheck, `govulncheck` sem vulnerabilidades alcançáveis, OpenAPI, Goose validate e build dos três binários.
- `docker compose --profile demo config --quiet`, `git diff --check` e os limites físicos passaram; nenhum arquivo Go de produção ou migration da Sprint 5 excede 200 linhas.

### Correções após Code Review — rodada 1

- Parents remotos sampled/unsampled agora obedecem à razão local; timeout zero, razões `NaN`/infinitas e todos os limites inválidos falham fechado.
- Chaos Lab limita oito cenários concorrentes, rejeita overflow imediatamente e mantém timeouts de servidor; o conjunto de chaves continua limitado a quatro.
- API/worker compartilham um único deadline absoluto de shutdown entre HTTP, core e flush. Falha de exportação permanece observacional e não mascara o encerramento.
- Transações tenant-scoped, claim e finalização possuem spans filhos seguros com resultados allowlisted, sem erro bruto, tenant, payload ou URL.
- A documentação exige ACL/rede privada para bind operacional não-loopback. O quickstart repete replay com chave e corpo idênticos e valida command, delivery, run e marcador duplicate sem mutação adicional.
- Após as correções, os probes focados passaram vinte vezes, inclusive sob race; o quickstart E2E, migrations `up/down/up`, `make integration` em PostgreSQL 17 e `make check` integral ficaram verdes.
- Na rodada 2, o nome do span HTTP passou a usar o mesmo método allowlisted das métricas/atributos; método remoto arbitrário resulta em `OTHER` e o canário não alcança nenhum sink.

### QA da Sprint 5

**Veredicto:** ✅ Aprovado em 2026-09-24.

- `make check` passou após os testes de QA com unitários, race detector, `vet`, Staticcheck, `govulncheck`, OpenAPI, Goose validate e build dos três binários.
- A regressão final em PostgreSQL 17.11 executou 224/224 testes e subtestes em 24 pacotes, com zero skip e zero falha; `make integration` preservou regressões S1–S4 e passou queue metrics, pipeline HTTP e `SIGTERM`.
- `make quickstart` concluiu HMAC, assinatura dupla, retry, DLQ, replay duplicado estável e telemetria em 43,28 segundos, com cleanup completo.
- Telemetria/config/probes/runtime/tenanttx/Chaos/HMAC passaram vinte repetições sob race; shutdown da API passou cinquenta repetições e scheduler focado vinte.
- A função `delivery_queue_metrics()` passou vinte repetições sob race, três ciclos completos de boundaries e `down-to 19 → up 20`; ACL, owner executor, `search_path` e timeout foram confirmados diretamente.
- Probes reais confirmaram superfície pública sem live/ready/metrics/pprof, listeners operacionais em loopback, liveness/metrics durante quarantine, readiness `503` e retorno a `200` após reconcile.
- O QA acrescentou somente testes para pprof/superfície pública, fail-closed de keyrings e spans DNS/connect parented/redacted. Nenhum código de produção foi modificado.
- Relatório completo: `docs/webhook-delivery-engine-qa-sprint-5.md`.

### Próximo gate

Sprint 5 aprovada em Code Review e QA, apta para commit e integração em `main`; em seguida, iniciar a Sprint 6 — Hardening e release de portfólio.

## Sprint 6 — Hardening e release de portfólio

- ✅ S6-01 — Suite adversarial final — implementada pelo Stack Agent
- ✅ S6-02 — Benchmarks e profiling — implementada pelo Stack Agent
- ✅ S6-03 — Containers, SBOM e scan — implementada pelo Stack Agent
- ✅ S6-04 — Runbook e demonstração — implementada pelo Stack Agent
- ✅ Code Review independente — aprovado na rodada 4 com zero blocker, warning ou
  suggestion
- ✅ Correções da rodada 1 — implementadas e validadas pelo Stack Agent
- ✅ Correções da rodada 2 — implementadas; probes focados e gates sem container verdes
- ✅ Correção da rodada 3 — manifesto de filesystem completo e testes negativos
- ✅ Rebuild, smoke, SBOM e scan — aprovados em daemon isolado, preservando os 13
  containers preexistentes de outros projetos
- ✅ QA independente — aprovado sem ressalvas
- ✅ Security audit OWASP A01–A10 — aprovado sem blocker ou warning
- ✅ Release local `v1.0.0` — changelog, commit final, artefatos exatos e tag anotada

### Evidências do Stack Agent

- A matriz HTTP cobre autenticação, scope correto/incorreto e isolamento cross-tenant
  para todas as sete rotas produtivas; o runner adversarial executa toda a árvore de
  testes em série e falha diante de qualquer skip.
- O harness usa PostgreSQL 17 em `tmpfs` e receptor loopback reais, credencial `0600`,
  HMAC verificado, IDs esperados distintos, estado final do banco e séries de
  RSS/heap/goroutines/backlog. Warmup separado e três rodadas de 1.000 eventos mediram
  medianas de 440,00 ingestões/s, p95 de ingestão 15,08 ms e 32,77 deliveries/s. O
  soak concluiu 3.000/3.000 IDs distintos, zero duplicata/missing/falha/backlog e 283
  amostras de sistema sem crescimento anômalo; o alvo de 100 deliveries/s não foi
  atendido e permanece risco residual documentado.
- API, worker, Chaos Lab e migrator possuem imagens multi-stage pinadas, distroless,
  non-root/read-only, sem capabilities, com `no-new-privileges` e `tmpfs`. O smoke
  valida healthchecks, roles/schema e o diff exato de cada filesystem contra a base
  pinada: somente o binário de serviço, ou Goose e as migrations, podem ser adicionados.
- Syft 1.52.0 gera CycloneDX/SPDX das imagens por ID imutável; Trivy 0.74.0 bloqueia
  High/Critical e Gitleaks 8.30.1 examina o histórico. `tools.tsv` preserva versões,
  assets e checksums esperados/observados, junto à metadata da base Trivy; ferramentas,
  diretórios e caches efêmeros são removidos depois do gate.
- `SECURITY.md`, runbook de incidentes/restore/rollback, demonstração, resultados de
  benchmark, supply chain e checklist de release registram premissas, procedimentos,
  limitações, trade-offs e riscos residuais sem expor material sensível.

### Correções após Code Review — rodada 1

- A CI executa `make adversarial` com quatro DSNs, falha diante de qualquer skip e
  inclui a matriz de autenticação, scope e tenant das sete rotas. O alvo nominal
  `make integration` também inclui essa matriz.
- O benchmark compara o conjunto exato de `delivery_id`; duplicata não mascara ID
  ausente. O listener é fechado e seus handlers são drenados antes do snapshot final;
  cada relatório registra unique/duplicates/missing/unexpected/invalid-signatures e só
  passa com banco em backlog zero, total exato em `succeeded` e zero falha.
- Warmup é excluído; benchmark normal exige três rodadas e publica
  mínimo/máximo/mediana/média/desvio/p95. Soak tem teto hard de 5.000, usa PG17 em
  `tmpfs`, coleta séries de RSS/heap/goroutines/backlog e falha nos thresholds de
  crescimento, monotonia ou backlog final.
- Smoke exporta base e alvo para arquivos verificados e valida API, worker, Chaos Lab e
  migrator com manifestos canônicos e allowlists positivas diferentes; hash de
  conteúdo herdado, tipo, modo, UID/GID e destino de link também são comparados.
  Extras, remoções, alteração de conteúdo/symlink/metadados, traversal, paths
  malformados e duplicados falham nos probes negativos.
- O runbook cobre comprometimento de pacote/imagem. `make rollback-rehearsal` prova
  downgrade/re-upgrade vazio e o guard transacional interno contra workspace,
  quarentena global e writer concorrente, mantendo Goose 20/schema v5/função/dados
  intactos. A evidência preserva `tools.tsv`, metadata Trivy e relatório Gitleaks junto
  de SBOMs/scans.

### Correções após Code Review — rodada 2

- O primeiro `Down` obtém locks incompatíveis com writers e recusa qualquer estado
  operacional v5, inclusive audit global e controle de restore não-default, antes do
  primeiro `REVOKE`/`DROP`; o preflight externo continua apenas como UX.
- O verificador de imagem abandonou denylist: canonicaliza os inventários completos da
  base pinada e do alvo e exige exatamente o diff positivo esperado por tipo de imagem.
- O benchmark separa `wait-all-expected` do snapshot: após convergência do banco, fecha
  o listener, drena handlers e contabiliza callbacks tardios e assinaturas inválidas.
- PostgreSQL 17 real confirmou downgrade vazio `20→19→20` e bloqueio atômico de
  workspace, quarentena global e writer concorrente. Os quatro inventários de imagem
  existentes e os probes negativos do comparador passaram antes de o daemon local
  apresentar erro de I/O; como a migration 20 mudou, rebuild/smoke/scans finais seguem
  obrigatórios no próximo ambiente Docker saudável.

### Correção após Code Review — rodada 3

- O inventário somente por path foi substituído por manifesto derivado diretamente do
  tar exportado. Paths herdados exigem igualdade de tipo, modo, UID/GID, destino de
  symlink e SHA-256 de arquivo regular. Os SQLs adicionados exigem o hash do checkout;
  somente os hashes dos binários construídos são wildcard explícita, com todos os seus
  metadados ainda estritos. A suíte negativa cobre conteúdo no mesmo path, symlink,
  tipo, modo, owner, extra, missing, traversal e duplicata sem acessar Docker.

### Validação final

- O candidato foi reconstruído em um profile Colima isolado; nenhum container ou
  volume dos demais projetos foi parado, reiniciado ou removido.
- `make check`, `make integration`, `make adversarial`, `make quickstart`,
  `make rollback-rehearsal`, `make container-smoke` e `make secret-scan` passaram.
- O smoke confirmou as quatro imagens hardened e seus manifestos canônicos. O gate de
  supply chain gerou oito SBOMs e encontrou zero High/Critical e zero segredo.
- A API pública recebeu headers de segurança em todas as respostas; HSTS permanece
  condicionado ao profile produtivo com TLS terminado no ingress.
- Relatórios finais: `docs/webhook-delivery-engine-qa-sprint-6.md` e
  `docs/webhook-delivery-engine-security-audit.md`.

### Próximo gate

MVP concluído e release local `v1.0.0` validada. Publicação em registry ou repositório
remoto permanece uma ação separada, pois este checkout não possui remote configurado.

## Evolução pós-MVP — demonstração produtiva no EasyPanel

- ✅ Compose produtivo separado, landing page em pt-BR e Swagger navegável.
- ✅ PostgreSQL isolado sem TCP, com socket Unix, SCRAM e secrets `0400`.
- ✅ Gerador de segredos e smoke de CI da topologia completa.
- ✅ Gates completos, revisão de segurança, commits incrementais e pull request público.
- ⏳ Merge, release `v1.1.0` e deploy na VPS pessoal via EasyPanel.

O escopo é uma demonstração de portfólio com dados sintéticos, não uma oferta
SaaS. A decisão de host único está em `ADR-011` e o procedimento em
`docs/easypanel-deployment.md`.
