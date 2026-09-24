# QA Report: Sprint 1 — Primeiro corte vertical

## Status: ✅ APROVADO

**Data:** 2026-09-24  
**Branch validada:** `feature/sprint-1-vertical-slice`  
**Ambiente:** Go 1.27.1 (`darwin/arm64`) e PostgreSQL 17.11 real, em contêiner descartável com as roles do projeto  
**Code Review de entrada:** aprovado no ciclo 3, com zero blockers e zero warnings

## Resumo executivo

A Sprint 1 passou nos gates estáticos, unitários, de corrida, integração com PostgreSQL real e E2E. Foram executados 66 eventos de teste unitário/subteste e cinco cenários de integração de alto nível, totalizando 71 resultados automatizados aprovados e zero falha de produto.

O fluxo completo `endpoint → evento → claim do worker → webhook HMAC → Chaos Lab → succeeded → timeline` foi exercitado com banco e roles reais. O mesmo teste confirmou isolamento cross-tenant, autorização por escopo, idempotência, limites de entrada e ausência de payload/segredo nas consultas.

## Validação estática e de build

| Check | Resultado |
|---|---|
| `make check` | ✅ Aprovado |
| `gofmt` e `go mod tidy -diff` | ✅ Sem diferenças |
| `go test ./...` | ✅ Aprovado |
| `go test -race ./...` | ✅ Aprovado |
| `go vet ./...` | ✅ Aprovado |
| Staticcheck | ✅ Aprovado |
| `govulncheck` | ✅ Nenhuma vulnerabilidade alcançável |
| Goose validate | ✅ Aprovado |
| Build de `api`, `worker` e `chaoslab` | ✅ Aprovado |
| `git diff --check` | ✅ Sem whitespace inválido |
| `docker compose --profile demo config --quiet` | ✅ Aprovado |
| Parse do OpenAPI 3.1 | ✅ Quatro rotas e timeline tipada presentes |

Não foram construídas imagens Docker nem baixadas dependências/imagens durante este QA, preservando o espaço em disco conforme a restrição operacional. A imagem `postgres:17` já presente foi reutilizada.

## Testes automatizados

| Área | Categoria | Evidência | Resultado |
|---|---|---|---|
| Configuração, auth, endpoint, evento, delivery, HMAC e plataforma | Small/Medium | 66 eventos de teste, incluindo subtestes | 66/66 ✅ |
| Bootstrap concorrente e revogação | Large/integração | `TestCredentialBootstrapIsSerializedAndRevocable` | 1/1 ✅ |
| RLS, workspace inativo, ACL e histórico append-only | Large/integração | `TestTenantContextAndAppendOnlyACL` | 1/1 ✅ |
| Idempotência concorrente | Large/integração | `TestConcurrentIdempotency`, 100 publicações simultâneas | 1/1 ✅ |
| Claim com snapshot incompleto | Large/integração | `TestClaimWithoutCompleteSnapshotDoesNotMutateDelivery` | 1/1 ✅ |
| API + worker + Chaos Lab | Large/E2E | `TestEndpointEventWorkerChaosLabSucceeded` | 1/1 ✅ |
| **Total** |  |  | **71/71 ✅** |

### Testes adicionados ou ampliados pelo QA

- `test/integration/z_e2e_test.go`: passou a validar `GET endpoint`, segredo exibido somente na criação, isolamento cross-tenant, 403 por escopo insuficiente, repetição idempotente, conflito 409, payload acima de 1 MiB, timeline sem material sensível e recusa de workspaces `suspended`/`deleting`.
- `internal/platform/config/config_test.go`: passou a provar que `WDE_ALLOW_HTTP_DESTINATIONS=true` é carregado no perfil local e recusado no perfil de produção.

## Banco, migrations e tenancy

| Cenário | Resultado |
|---|---|
| Migration do zero `up → down → up` | ✅ Versões `2 → 1 → 2` |
| Roles reais sem superuser/create role/create DB/replication/bypass RLS | ✅ Oito roles verificadas |
| Roles executoras `NOLOGIN` | ✅ Verificado |
| Owner das tabelas | ✅ `wde_owner` |
| RLS + `FORCE ROW LEVEL SECURITY` | ✅ Em todas as dez tabelas tenant-scoped |
| Consulta sem tenant | ✅ Zero linhas |
| Workspace `suspended` ou `deleting` | ✅ Auth recusada e RLS impede leitura/escrita |
| DML direto em `delivery_attempts` | ✅ Negado para API e worker |
| Funções privilegiadas | ✅ Owners dedicados, `search_path=pg_catalog`, sem `PUBLIC EXECUTE` |
| `current_workspace_id()` | ✅ Executável apenas pela role da API |
| Claim sem segredo/snapshot completo | ✅ Delivery permanece `pending`, sem tentativa criada |

## Cobertura por task da Sprint 1

| Task | Critérios validados | Status |
|---|---|---|
| S1-01 — Schema tenant-safe | up/down/up, FKs/ACLs, RLS fail-closed, roles reais, workspace inativo, histórico protegido | ✅ |
| S1-02 — Bootstrap e API key | advisory lock concorrente, emissão única, arquivo exclusivo `0600`, rollback com limpeza, dummy verifier, escopos e revogação | ✅ |
| S1-03 — Endpoint | create/get real, URL cifrada, segredo somente na criação, `problem+json`, cross-tenant invisível e flag HTTP | ✅ |
| S1-04 — Evento | evento+delivery atômicos, 100 requisições concorrentes para um evento/uma delivery, repetição estável, 409 e 413 sem persistência | ✅ |
| S1-05 — Worker 1 | claim atômico, tentativa registrada, HTTP fora da transação, finalização 2xx e snapshot inválido sem mutação | ✅ |
| S1-06 — HMAC v1 | vetor fixo independente, base64url, comparação constante e adulteração de timestamp, event ID, delivery ID, key ID, assinatura e corpo | ✅ |
| S1-07 — Chaos Lab/timeline | receptor local verifica assinatura, E2E chega a `succeeded`, tentativa aparece e resposta não expõe payload, headers, ciphertext ou segredo | ✅ |

## Testes práticos via terminal

| Teste | Método | Resultado |
|---|---|---|
| Bootstrap pelo binário | `bin/api credentials bootstrap --output-file=...` | ✅ Arquivo novo `0600`, workspace/chave únicos |
| Segunda inicialização | mesmo comando em arquivo diferente | ✅ Recusada e sem arquivo residual |
| Saída não-TTY sem arquivo | bootstrap sem `--output-file` | ✅ Recusada antes da emissão |
| Revogação pelo binário | `bin/api credentials revoke --prefix=...` | ✅ Status `revoked` e `revoked_at` persistidos |
| Create/Get endpoint | HTTP real via `httptest.Server` com PostgreSQL real | ✅ 201/200; consulta não contém segredo |
| Escopo insuficiente | chave somente `deliveries:read` em `POST /v1/endpoints` | ✅ 403 `application/problem+json` |
| Cross-tenant | chave B consulta endpoint A | ✅ 404 indistinguível |
| Repetição idempotente | mesmo conteúdo lógico em ordem JSON diferente | ✅ Mesmo event/delivery ID e `duplicate=true` |
| Conflito idempotente | mesma chave com conteúdo diferente | ✅ 409 `application/problem+json` |
| Payload acima de 1 MiB | corpo com 1 MiB + 1 byte | ✅ 413 antes do store |
| Fluxo completo | API → banco → worker → Chaos Lab → consulta | ✅ `succeeded`, uma tentativa `success` |

## Cobertura de código observada

As porcentagens abaixo representam somente o comando unitário por pacote; os testes PostgreSQL/E2E foram medidos separadamente e não entram nesses números.

| Pacote/capacidade | Cobertura |
|---|---:|
| `internal/platform/config` | 81,5% |
| `internal/platform/logging` | 83,8% |
| `internal/platform/operational` | 92,9% |
| `internal/platform/problem` | 96,0% |
| `internal/signing` | 87,5% |
| `internal/endpoint` | 51,8% |
| `internal/delivery` | 47,9% |
| `internal/event` | 44,8% |
| `internal/auth` | 44,0% |

Não existe threshold global de cobertura configurado nesta sprint. A aprovação se baseia nos comportamentos e riscos cobertos pela suíte unitária, integração real e E2E, e não em uma porcentagem agregada.

## Acessibilidade e validação visual

Não aplicável. O MVP desta sprint é API-first, sem frontend ou páginas navegáveis. O contrato HTTP, headers, problem details e respostas JSON foram validados programaticamente.

## Falhas encontradas

Nenhuma falha de produto permaneceu. Durante a ampliação do E2E, uma expectativa do novo teste usou inicialmente o código genérico `forbidden`; a implementação retorna corretamente o código estável e mais específico `insufficient_scope`. O teste foi corrigido e toda a suíte passou novamente.

## Restrição operacional

- O build dos três binários foi executado.
- O Compose foi renderizado e validado.
- As imagens da aplicação não foram reconstruídas por limite de armazenamento e por instrução explícita do escopo de QA. Esse caminho já está materializado no job `compose-smoke` da CI.
- Nenhuma validação dependente de browser foi omitida, pois não existe UI nesta sprint.

## Veredicto

**QA Sprint 1 aprovado.** Todos os critérios do primeiro corte vertical foram comprovados sem falhas pendentes. A branch pode ser integrada e a execução pode avançar para a Sprint 2.
