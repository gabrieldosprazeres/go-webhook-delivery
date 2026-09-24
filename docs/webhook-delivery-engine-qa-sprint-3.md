# QA Report: Sprint 3 — Tenancy, replay e operação

**Data:** 2026-09-24  
**Branch:** `feature/sprint-3-tenancy-replay-ops`  
**Status:** ✅ **APROVADO**

## Veredito binário

**APROVADO.** Os itens `S3-01` a `S3-04` foram validados sem falhas pendentes. A regressão final sem cache executou **147/147 testes e subtestes**, em **18 pacotes testados**, com **zero skip** e **zero falha**. O pipeline real de integração passou com as quatro roles PostgreSQL, a matriz S3 passou sob race detector e o stress final executou **160/160 cenários S3** no mesmo processo sem flakiness.

## Ambiente

| Item | Evidência |
|---|---|
| Go | `go1.27.1 darwin/arm64` |
| PostgreSQL | `17.11 (Debian 17.11-1.pgdg13+2)` |
| Imagem | `postgres:17`, já disponível localmente, digest `sha256:f4c66b820c6f974249089d3d16d86a3698eae11e8746eb6644b2271031e91232` |
| Bancos de QA | Containers isolados `wde-s3-qa-pg` e `wde-s3-qa-clean`, ambos em `tmpfs` e portas loopback |
| Docker | Nenhuma imagem foi baixada ou construída |
| Frontend | Não aplicável: produto API-first, sem UI; a11y, viewport e validação visual não se aplicam |

## Validação estática e gates

| Check | Resultado |
|---|---|
| `make check` | ✅ Passou após as correções exclusivas do harness |
| `gofmt` | ✅ Sem divergências |
| `go mod tidy -diff` | ✅ Sem divergências |
| `go test ./...` | ✅ Passou |
| `go test -race ./...` | ✅ Passou |
| `go vet ./...` | ✅ Passou |
| Staticcheck | ✅ Passou |
| `govulncheck` | ✅ Nenhuma vulnerabilidade alcançável |
| OpenAPI lint/resolve/validate | ✅ Passou |
| Goose validate | ✅ Passou |
| Build `api`, `worker`, `chaoslab` | ✅ Passou |
| `docker compose --profile demo config --quiet` | ✅ Passou |
| `git diff --check` | ✅ Passou |

## Resumo das execuções

| Execução | Escopo | Resultado |
|---|---|---|
| `make integration` | Bootstrap, 15 regressões S1/S2, 8 cenários S3, pipeline público e `SIGTERM` | ✅ Sem falhas ou skips |
| `go test ./... -count=1 -p=1` com as quatro DSNs reais | Regressão integral sem cache e PostgreSQL real | **147/147**, 18 pacotes, 0 skip ✅ |
| 8 cenários S3 com `-count=20` | Tenant transaction, quotas, fan-out, paginação, replay e auditoria | **160/160** ✅ |
| Pipeline público com `-count=20` | Edge limiter → auth → quota → scope → handler | **20/20 execuções; 80/80 subcenários** ✅ |
| Quota concorrente + replay concorrente com `-count=50` | Contenção global exata e idempotência concorrente | **100/100** ✅ |
| Fan-out/paginação + replay com `-count=20` | Limites e histórico sob banco acumulado | **40/40** ✅ |
| Migration boundaries com `-count=5` | `up/down/up`, contratos/grants e fail-closed | **5/5** ✅ |
| Plano isolado + E2E legado com `-count=5` | Índice representativo e jornada completa | **10/10** ✅ |
| Matriz S3 com `-race` e PostgreSQL real | Corridas de dados na aplicação durante integração | **8/8** ✅ |

Não existe threshold formal de cobertura Go para esta Sprint. A aprovação usa critérios de aceite, integração real, race detector, stress e controles SQL independentes, sem perseguir percentual arbitrário.

## Cobertura por item da Sprint

### S3-01 — RLS e funções privilegiadas completas

| Cenário | Evidência | Resultado |
|---|---|---|
| GUC tenant-scoped na mesma transação/conexão | `TestTenantTransactionDoesNotLeakAfterCommitRollbackOrPanic` | ✅ |
| Pool de uma conexão sem vazamento após commit/rollback/panic | Mesmo teste, repetido na matriz de stress | ✅ |
| Isolamento cross-tenant e ausência de contexto no pool | Consultas retornam somente o workspace corrente; depois da devolução, zero tenant/linhas | ✅ |
| Funções com mínimo privilégio | Owners dedicados `NOLOGIN`, `SECURITY DEFINER`, `search_path=pg_catalog`, sem `PUBLIC EXECUTE` | ✅ |
| Separação de capacidades | Quota, replay e auditoria não possuem privilégios cruzados | ✅ |
| RLS materializada | `ENABLE` + `FORCE RLS` em `audit_events`, `rate_limit_buckets` e `replay_commands` | ✅ |

### S3-02 — Quotas e limites persistentes

| Cenário | Evidência | Resultado |
|---|---|---|
| Dimensões global/workspace/API key/recurso | `TestPersistentQuotaIsAtomicAcrossDimensionsRestartAndExpiry` | ✅ |
| Reinício não zera quota | Nova instância do limiter preserva os buckets PostgreSQL | ✅ |
| Falha de uma dimensão reverte todas | Soma dos buckets não cresce após limite do recurso | ✅ |
| Expiração | Nova janela aceita consumo após expirar; relógio usa `transaction_timestamp()` | ✅ |
| Contenção global | Exatamente 8 de 32 requisições aceitas; 24 recusadas | ✅ em 50 repetições |
| Fan-out | 101 destinos são recusados sem evento ou delivery parcial | ✅ |
| Paginação | Limites 50/100, página seguinte, rejeição de 101 e cursor inválido | ✅ |
| Cursor tenant-bound/tamper-evident | Cursor de outro tenant e cursor adulterado retornam `invalid_page` | ✅ |
| Pipeline pré-auth/autenticado | Token inválido `401 → 429`; chave sem escopo `403 → 429` | ✅ |
| Anti-poisoning | Mesmo UUID de delivery entre atacante e vítima gera buckets distintos | ✅ |
| Origem não forjável | `X-Forwarded-For` não altera a origem observada | ✅ |

### S3-03 — Replay por geração

| Cenário | Evidência | Resultado |
|---|---|---|
| Novo run e sequência histórica monotônica | Histórico `(run 1, attempt 1, seq 1) → (run 2, attempt 1, seq 2)` | ✅ |
| Idempotência concorrente | Duas solicitações idênticas produzem um comando/run/audit; uma resposta é duplicate | ✅ |
| Fingerprint divergente | Mesma chave com motivo diferente produz um sucesso e um conflito estável | ✅ |
| Contrato HTTP | `403` sem scope, `404` cross-tenant, `202` idempotente e `409` divergente | ✅ |
| Payload expurgado | Replay recusado sem comando, audit ou transição parcial | ✅ |
| Atomicidade | Comando, transição e auditoria confirmam juntos | ✅ |
| Stress | Replay isolado 20/20 e combinado com quota 50/50 | ✅ |

### S3-04 — Auditoria append-only

| Cenário | Evidência | Resultado |
|---|---|---|
| Ações sensíveis auditadas | Bootstrap/revogação, endpoint create e replay cobertos por integração | ✅ |
| Sem update/delete runtime | API, worker e admin não possuem nem executam `UPDATE`/`DELETE` | ✅ |
| Snapshot sobrevive purge | Actor/resource continuam consultáveis após remover API key e workspace | ✅ |
| Canários ausentes | Zero ocorrência de URL privada, `ciphertext` ou `payload` nos eventos de auditoria | ✅ |
| Sem FK para entidade expurgável | Snapshot histórico permanece independente das entidades removidas | ✅ |

## Banco, migrations e segurança

| Verificação | Resultado |
|---|---|
| Migrations do zero `000001`–`000010` | ✅ Goose 10 |
| Schema lógico | ✅ Versão 4 |
| Boundaries físicos 7/8/9 | ✅ Mantêm schema lógico v3 e staging sem grants runtime |
| Boundary físico 10 | ✅ Publica entrypoints e schema lógico v4 atomicamente |
| Downgrade 10→9→8→7→6 e cadeia anterior | ✅ Runtime v4 falha fechado fora do contrato completo |
| Re-upgrade até 10 | ✅ Restaura somente o contrato v4 completo |
| Roles executor | ✅ `NOLOGIN`, sem superuser/createdb/createrole/replication/bypassrls |
| Auditoria | ✅ Append-only; zero canários sensíveis |
| Recursos temporários | ✅ Zero databases `wde_boundary_*`, `wde_claim_plan_*` ou `wde_bootstrap_*` remanescentes |

Consulta independente final:

```text
schema_logico=4 | goose=10
append_audit_event owner=wde_audit_executor  definer=true  search_path=pg_catalog  public_execute=false
consume_quota      owner=wde_quota_executor  definer=true  search_path=pg_catalog  public_execute=false
request_replay     owner=wde_replay_executor definer=true  search_path=pg_catalog  public_execute=false
audit_events/rate_limit_buckets/replay_commands: RLS=true, FORCE RLS=true
API/worker/admin: audit UPDATE=false, DELETE=false
forbidden_audit_canaries=0
```

## Regressão S1/S2

- Bootstrap/revogação, RLS/ACL, idempotência concorrente e snapshot completo permaneceram verdes.
- Claim/fairness, locks, lease/fencing, retry/DLQ, status hostil e histórico permaneceram verdes.
- O E2E `endpoint → event → worker → Chaos Lab → succeeded` passou após isolamento explícito das fixtures.
- `SIGTERM` do worker passou.
- HMAC, auth, endpoint, evento, configuração, logging, problem details e OpenAPI permaneceram verdes.

## Falhas de QA encontradas e corrigidas

Nenhum defeito de produção permaneceu aberto. Três falhas de isolamento do harness foram encontradas sob execução mais agressiva e corrigidas somente em testes:

1. Cinco testes S3 descartavam o terceiro pool criado por `reliabilityPools`; health-check goroutines acumulavam em `-count=20` e, após vários ciclos, o claim atingia 2 s. Todos os pools agora são fechados. A matriz completa passou **160/160** no mesmo processo e no banco já carregado.
2. O teste de plano dependia da distribuição global do banco compartilhado; milhares de fixtures antigas podiam tornar `Seq Scan` racional. O cenário agora cria, migra e remove database próprio. Passou 5/5 em banco acumulado.
3. O E2E legado não suspendia workspaces ativos deixados por cenários anteriores e o worker podia disputar outro job. O fixture agora isola a jornada antes de publicar. Plano isolado + E2E passaram 10/10.

Essas correções não alteram código de produção, migrations nem contrato público.

## Testes práticos

| Teste | Método | Resultado |
|---|---|---|
| Build | `make check` / target `build` | ✅ |
| PostgreSQL real | Dois containers locais `postgres:17` em `tmpfs` | ✅ |
| Migrations | `make migrate-up`, `make migrate-status`, boundaries `up/down/up` | ✅ |
| Integração com roles reais | `make integration` | ✅ |
| Pipeline HTTP de produção | `newPublicServer` com edge/auth/quota/scope/handler e PostgreSQL real | ✅ |
| Regressão integral sem cache | `go test ./... -count=1 -p=1` com DSNs reais | ✅ 147/147 |
| Compose | `docker compose --profile demo config --quiet` | ✅ |
| Diff | `git diff --check` | ✅ |

## Riscos residuais

- O limiter de origem é local por instância e best-effort, conforme arquitetura; quotas autenticadas PostgreSQL são o controle distribuído do MVP.
- O PostgreSQL é o ponto de coordenação das quotas globais e deverá ser acompanhado por benchmark/observabilidade nas Sprints 5 e 6.
- A suíte de integração exige database descartável e roles reais. O QA tornou os cenários de plano e bootstrap independentes do estado global para evitar falso positivo/negativo em reexecuções.
- Containers da aplicação não foram reconstruídos localmente por restrição de armazenamento; os binários foram compilados e exercitados, e o contrato Compose foi validado sem download/build de imagem.

## Pendências

Nenhuma falha funcional, de segurança, concorrência, migration ou regressão permanece aberta para a Sprint 3.

## Veredito

**QA Sprint 3 aprovado.** `S3-01`, `S3-02`, `S3-03` e `S3-04` atendem aos critérios de aceite. A branch está apta para commit, integração em `main` e início da Sprint 4.
