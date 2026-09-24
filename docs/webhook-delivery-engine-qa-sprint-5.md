# QA Report: Sprint 5 — Observabilidade e demonstração

**Data:** 2026-09-24  
**Branch:** `feature/sprint-5-observability-demo`  
**Status:** ✅ **APROVADO**

## Veredito binário

**APROVADO.** Os itens `S5-01` a `S5-04` foram validados sem falha pendente. A regressão final com PostgreSQL 17 real e as quatro roles executou **224/224 testes e subtestes**, em **24 pacotes testados**, com **zero skip** e **zero falha**. `make check`, `make integration`, `make quickstart`, race detector, stresses focados, migrations, probes reais, Compose e validação de diff passaram.

O Code Review independente fornecido como entrada deste gate encerrou a rodada 2 com **zero blockers e zero warnings**.

## Ambiente

| Item | Evidência |
|---|---|
| Go | `go1.27.1 darwin/arm64` |
| PostgreSQL | `17.11 (Debian 17.11-1.pgdg13+2)` |
| Imagem | `postgres:17`, já disponível localmente, digest `sha256:f4c66b820c6f974249089d3d16d86a3698eae11e8746eb6644b2271031e91232` |
| Banco principal de QA | Container `wde-s5-qa-pg`, porta loopback `55437`, dados em `tmpfs` de 768 MiB |
| Quickstart | Projeto Compose efêmero próprio, credencial sintética e cleanup automático |
| Frontend | Não aplicável: produto API-first, sem UI; a11y, viewport e validação visual não se aplicam |

O host iniciou com apenas 589 MiB livres. Foram removidos somente cache de build/teste Go recompilável e a imagem efêmera de migration criada pelo quickstart; nenhum projeto, dependência versionada ou dado do usuário foi removido.

## Validação estática e gates

| Check | Resultado |
|---|---|
| `make check` final | ✅ Passou |
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
| `make integration` | Bootstrap, regressões S1–S4, queue metrics S5, pipeline público e `SIGTERM` | ✅ Sem falhas ou skips |
| `go test ./... -count=1 -p=1` com quatro DSNs reais | Regressão final em PostgreSQL real | **224/224**, 24 pacotes, 0 skip ✅ |
| `make quickstart` | HMAC, rotação, replay, retry, DLQ e métricas | **43,28 s**, abaixo do orçamento de 10 min ✅ |
| Telemetria/config/probes/runtime/tenanttx/Chaos/HMAC com `-count=20 -race` | Bounded dimensions, sampling, canários, lifecycle e concorrência | ✅ |
| Shutdown da API com `-count=50 -race` | Deadline global e falha de exporter | **100/100 cenários** ✅ |
| Scheduler focado com `-count=20 -race` | Carga, hard deadline, drain e cancelamento | **80/80 cenários** ✅ |
| Queue metrics com `-count=20 -race` | Agregado PostgreSQL e ACL worker-only | **20/20** ✅ |
| Migration boundaries com `-count=3` | Subida 3→20, descida 19→2 e re-upgrade | **3/3** ✅ |
| Testes QA pós-review com `-count=20 -race` | Superfície pública/pprof, keyrings e spans DNS/connect | ✅ |

Não existe threshold formal de cobertura percentual Go nesta Sprint. O gate usa critérios de aceite, testes de comportamento, PostgreSQL real, race detector, stress, probes dos binários e controles SQL independentes.

## Cobertura por item da Sprint

### S5-01 — Métricas Prometheus e traces OpenTelemetry

| Cenário | Evidência | Resultado |
|---|---|---|
| Métricas por processo | API e worker expõem registries próprios somente no listener operacional | ✅ |
| Labels bounded | Rotas, métodos, status, outcomes, categorias e purge passam por conjuntos fechados; desconhecidos viram `OTHER`/`other` | ✅ |
| Canários | Payload, API key, HMAC, URL/query, headers, resposta, erro bruto e método remoto arbitrário não alcançam métricas, spans ou logs | ✅ |
| Métricas de negócio | HTTP/ingest, attempt/duration/retry/DLQ/fencing, queue/oldest/sample, workers/inflight e purge/lag presentes | ✅ |
| Sampling remoto | Parents remotos sampled e unsampled obedecem à razão local; autenticação não amplia sampling | ✅ 20x sob race |
| Propagação | Somente `traceparent`; baggage e tracestate recebidos são descartados | ✅ |
| Configuração | Timeout zero/fora do limite, `NaN`/inf e ratio inválida falham fechados; produção exige OTLP HTTPS sem credenciais/query/fragment | ✅ |
| No-op/exporter indisponível | Ausência de endpoint mantém métricas; collector `503` não interrompe negócio | ✅ |
| Shutdown | HTTP/core/flush compartilham deadline absoluto; falha do flush é sanitizada e não substitui o resultado do negócio | ✅ 50x sob race |
| Spans de persistência | Transaction, claim, attempt e finalize correlacionados e com resultado allowlisted | ✅ |
| Spans de rede/HTTP | Ingresso HTTP bounded; resolve/connect são filhos do attempt e não registram host/IP canário | ✅ 20x sob race |

### S5-02 — Probes e superfície operacional

| Cenário | Evidência | Resultado |
|---|---|---|
| Separação de listeners | API pública em `127.0.0.1:18180`; API ops em `127.0.0.1:19190`; worker ops em `127.0.0.1:19191` | ✅ |
| Superfície pública | `/livez`, `/readyz`, `/metrics` e `/debug/pprof/` retornaram `404` | ✅ |
| Superfície operacional | `/livez`, `/readyz` e `/metrics` retornaram `200`; `/debug/pprof/` retornou `404` | ✅ |
| Liveness mínima | Permaneceu `200` durante restore quarantine; não consulta banco nem destino | ✅ |
| Readiness API/worker | PostgreSQL, role/schema v5, keyrings, quarantine e, no worker, scheduler/retenção são considerados | ✅ |
| Keyrings | Ausência do primary em payload ou signing torna `Materials.Ready` indisponível | ✅ 20x sob race |
| Quarantine/reconcile real | Quarantine: live/metrics `200`, ready `503`; reconcile: ready `200/200` | ✅ |
| Scheduler/retenção | Worker real publicou `wde_active_workers 1`, `wde_queue_sample_success 1`, fila/oldest e purge lag; testes degradados derrubam readiness | ✅ |
| Bind | Todos os listeners observados exclusivamente em IPv4 loopback | ✅ |
| Shutdown | API e worker encerraram limpos por sinal, com log de `service stopped` e exit code 0 | ✅ |

### S5-03 — Chaos Lab completo

| Cenário | Evidência | Resultado |
|---|---|---|
| Success e fail-N | Duas respostas `503` seguidas de `204` determinístico | ✅ |
| Timeout | Respeita cancelamento e quickstart conduz retries até DLQ | ✅ |
| Rate limit | `429` com `Retry-After` determinístico | ✅ |
| Falha permanente | `400` sem retry | ✅ |
| Verificação HMAC | Rejeita assinatura ausente/inválida e aceita corpo bruto válido | ✅ |
| Rotação | Exige todas as chaves configuradas e valida assinatura dupla | ✅ |
| Limites | Máximo de quatro chaves e oito cenários simultâneos | ✅ |
| Overflow | Nona execução retorna `429` imediatamente com `Retry-After: 1` | ✅ |
| Race/leaks | Slots, contador ativo e peak retornam ao estado esperado após carga | ✅ 20x sob race |
| Estado seguro | Expõe somente contadores/status; nenhum payload ou segredo | ✅ |

### S5-04 — OpenAPI e quickstart

| Cenário | Evidência | Resultado |
|---|---|---|
| Jornada ≤10 min | `make quickstart` terminou em **43,28 s** | ✅ |
| Credencial sintética | Arquivo temporário `0600`, diretório `0700`, sem impressão de API key/secret/payload | ✅ |
| Entrega assinada | Evento local chegou com HMAC válida | ✅ |
| Rotação | Chaos Lab contou duas assinaturas válidas no overlap | ✅ |
| Retry/DLQ | Fail-N terminou em sucesso; timeout esgotou attempts em `dead_letter` | ✅ |
| Replay duplicado | Mesma chave/corpo preservou command, delivery e run, com `duplicate=true` | ✅ |
| Consumidor de referência | Corpo bruto, comparação constante, janela de cinco minutos, adulteração e timestamp expirado cobertos | ✅ 20x sob race |
| OpenAPI | Contrato carregado, referências resolvidas e validação semântica aprovados | ✅ |
| Documentação | README, quickstart, curl, HMAC, superfícies e limitações coerentes; nenhuma promessa de exactly-once | ✅ |
| Cleanup | Zero container, volume, rede ou diretório temporário `wde-demo` após a execução | ✅ |

## Migration 000020 e segurança SQL

| Verificação | Resultado |
|---|---|
| Migrations do zero `000001`–`000020` | ✅ Goose 20 |
| Schema lógico | ✅ Permanece versão 5 |
| Boundary 19 | ✅ Função agregada ausente; contrato v5 anterior preservado |
| Boundary 20 | ✅ `delivery_queue_metrics()` publicada e concedida somente ao worker |
| `down-to 19 → up 20` no banco principal | ✅ Função removida e recriada sem alterar schema lógico |
| Boundary completo `up/down/up` | ✅ 3/3 ciclos em databases isolados |
| Owner | ✅ `wde_worker_executor`, `NOLOGIN`, sem privilégios administrativos ou bypass RLS |
| Função | ✅ `SECURITY DEFINER`, `search_path=pg_catalog`, `statement_timeout=2s` |
| ACL | ✅ worker=true; API/admin/PUBLIC=false |
| Cardinalidade | ✅ Retorna apenas ready count e oldest seconds agregados, sem filtros ou IDs |
| Recursos temporários | ✅ Zero databases boundary/restore/claim-plan/bootstrap remanescentes |

Consulta independente final:

```text
schema_logico=5 | goose=20
delivery_queue_metrics: owner=wde_worker_executor | SECURITY DEFINER=true
search_path=pg_catalog | statement_timeout=2s
EXECUTE worker=true | api=false | admin=false | public=false
wde_worker_executor: NOLOGIN, sem superuser/createdb/createrole/replication/bypassrls
temporary_databases=0
```

## Regressão S1–S4

- Bootstrap/revogação, RLS/ACL, idempotência concorrente e snapshot completo permaneceram verdes.
- Claim/fairness, locks, lease/fencing, retry/DLQ, status hostil, scheduler e shutdown permaneceram verdes.
- Tenant transaction, quotas, fan-out, cursores, replay e auditoria permaneceram verdes.
- Anti-SSRF, AEAD v1/v2, rotação, retenção, purge, restore quarantine e reconcile permaneceram verdes.
- O pipeline público `edge limiter → auth → quota → scope → handler` e o E2E `endpoint → event → worker → Chaos Lab → succeeded` permaneceram verdes.

## Testes adicionados pelo QA

Nenhum código de produção foi alterado. O QA ampliou somente testes/harness:

1. A superfície pública agora prova `404` também para `/livez`, `/metrics` e `/debug/pprof/`.
2. O listener operacional prova explicitamente ausência de `pprof`.
3. `Materials.Ready` prova fail-closed sem o primary de qualquer keyring.
4. O dialer prova spans `resolve` e `connect` parented ao attempt, sem host/IP canário em atributos.

Todos passaram vinte vezes sob race detector e foram incluídos no `make check` final.

## Ocorrências de ambiente investigadas

- A primeira repetição do `make check` final falhou no linker com `no space left on device`. Não houve erro de código. Após remover somente cache Go recompilável e a imagem efêmera de migration, o mesmo gate passou integralmente.
- Reiniciar o container PostgreSQL com `/var/lib/postgresql/data` em `tmpfs` reinicializa propositalmente o cluster. As migrations foram reaplicadas e a regressão final 224/224 ocorreu depois dessa reinicialização, em banco limpo.

## Riscos residuais

- O worker é fail-fast quando o loop de claim perde PostgreSQL; o deploy precisa manter política de restart e readiness para retirar a instância antes da reposição. Liveness continua process-only enquanto o processo está ativo.
- OTLP em produção depende de collector HTTPS, autenticação de infraestrutura e ACL/rede privada para listeners operacionais não-loopback.
- Prometheus/OTel foram validados funcionalmente e sob race, mas metas de throughput/cardinalidade prolongada pertencem aos benchmarks da Sprint 6.
- O quickstart usa uma imagem de migration construída localmente a partir de bases já presentes. Em máquina fria, downloads entram no orçamento documentado de dez minutos.
- Imagens finais de API/worker/Chaos Lab não foram reconstruídas neste QA por restrição de armazenamento; binários foram compilados/exercitados e o Compose foi validado.

## Pendências

Nenhuma falha funcional, de segurança, concorrência, migration, observabilidade, quickstart ou regressão permanece aberta para a Sprint 5.

## Veredito

**QA Sprint 5 aprovado.** `S5-01`, `S5-02`, `S5-03` e `S5-04` atendem aos critérios de aceite. A branch está apta para commit, integração em `main` e início da Sprint 6.
