# QA Report: Sprint 2 — Confiabilidade e concorrência

**Data:** 2026-09-24  
**Branch:** `feature/sprint-2-reliability-concurrency`  
**Status:** ✅ **APROVADO**

## Veredito binário

**APROVADO.** Os quatro itens da Sprint 2 (`S2-01` a `S2-04`) foram validados sem falhas pendentes. A regressão final executou **121/121 casos e subcasos**, em **16 pacotes**, com **zero skip** e **zero falha**. A suíte real de integração passou em **17/17 cenários**, o stress de concorrência em **100/100 execuções** e o cenário de fairness com slot unitário em **80/80 processos isolados**.

## Ambiente e restrições

| Item | Evidência |
|---|---|
| Go | Toolchain fixada pelo projeto; `make check` compilou os três binários |
| PostgreSQL | `17.11 (Debian 17.11-1.pgdg13+2)` |
| Imagem | `postgres:17`, já disponível localmente, digest `sha256:f4c66b820c6f974249089d3d16d86a3698eae11e8746eb6644b2271031e91232` |
| Banco de QA | Container isolado `wde-s2-qa-agent`, dados em `tmpfs`, porta loopback `55432` |
| Docker | Nenhuma imagem foi baixada ou construída; somente a imagem PostgreSQL local foi usada |
| Frontend | Não aplicável: produto API-first, sem UI na Sprint 2; a11y, viewport e validação visual não se aplicam |

## Validação estática e gates

| Check | Resultado |
|---|---|
| `make check` | ✅ Passou após os testes de QA |
| `gofmt` | ✅ Sem divergências |
| `go mod tidy -diff` | ✅ Sem divergências |
| `go test ./...` | ✅ Passou |
| `go test -race ./...` | ✅ Passou |
| `go vet ./...` | ✅ Passou |
| Staticcheck | ✅ Passou |
| `govulncheck` | ✅ Nenhuma vulnerabilidade alcançável |
| Goose validate | ✅ Passou |
| Build `api`, `worker`, `chaoslab` | ✅ Passou |
| `make compose-config` | ✅ Passou |
| `git diff --check` | ✅ Passou |

## Resumo dos testes

| Execução | Escopo | Resultado |
|---|---|---|
| `go test -count=1 -json ./...` com as quatro DSNs reais | Regressão completa, incluindo Sprint 1 e PostgreSQL | **121/121**, 16 pacotes, 0 skip, 0 falha ✅ |
| `make integration` em banco já reutilizado | Boundaries, ACL/RLS, concorrência, lease, retry, E2E e SIGTERM | **17/17** ✅ |
| Cinco cenários de concorrência com `-count=20` | Fairness, locks, slot 1, multiworker e capacidade de endpoint | **100/100** ✅ |
| Slot unitário em processos separados | Fairness persistente sem estado global entre processos | **80/80** ✅ |
| `go test -cover ./internal/delivery ./cmd/worker` | Cobertura observacional | `internal/delivery`: **70,4%**; `cmd/worker`: **0,0%** por o comportamento ser exercitado via subprocesso |

Não há threshold formal de cobertura Go definido para esta Sprint. A aprovação se baseia nos critérios de aceite, testes de integração reais e gates globais, não em uma meta percentual arbitrária.

## Cobertura por item da Sprint

### S2-01 — Claim em batch com fairness

| Cenário | Evidência | Resultado |
|---|---|---|
| Batch limitado à capacidade livre | `TestSchedulerNeverClaimsBeyondFreeCapacity` | ✅ |
| `FOR UPDATE SKIP LOCKED` e workers concorrentes sem claim duplicado | `TestBatchClaimFairnessAndConcurrentWorkers` | ✅ |
| Limite por workspace e endpoint | Mesmo teste; batch de 6 resulta em 2+2 entre tenants e no máximo 2 por endpoint | ✅ |
| Workspace bloqueado não bloqueia tenant independente | `TestLockedWorkspaceDoesNotBlockIndependentClaim`, progresso <500 ms | ✅ |
| Fairness persistente multi-ciclo com slot 1 | `TestPersistentFairnessAcrossSingleSlotCycles` | ✅ |
| Fairness persistente multiworker com slot 1 | `TestPersistentFairnessWithConcurrentSingleSlotWorkers` | ✅ |
| Endpoint na capacidade não bloqueia endpoint saudável | `TestEndpointCapacityDoesNotStarveHealthyEndpoint` | ✅ |
| Plano de claim limitado por índice | `TestClaimPlanUsesReadyIndexAtRepresentativeScale`, 5.000 jobs, `deliveries_ready_idx`, sem `Seq Scan on deliveries` | ✅ |
| Stress | Cinco cenários × 20 repetições e slot 1 em 80 processos isolados | ✅ |

### S2-02 — Lease, fencing e recuperação

| Cenário | Evidência | Resultado |
|---|---|---|
| Token de fencing monotônico | Segundo claim possui token maior que o primeiro | ✅ |
| Finalização obsoleta afeta zero linhas | `TestLeaseRecoveryFencingAndAbandonedAttempt` | ✅ |
| Attempt anterior é abandonado/stale | Histórico preserva `abandoned/stale`, seguido de `completed/success` | ✅ |
| Recuperação ≤ TTL + 5 s | Lease real de 20 s; recuperação ocorreu imediatamente após expiração e dentro da margem de 5 s | ✅ |
| Nenhum `max+1` após crashes | `TestRepeatedCrashesStopAtMaximumAttempts` | ✅ |
| Nenhum `max+1` após retries | `TestRetryHistoryDeadLetterAndNeverMaxPlusOne` | ✅ |

### S2-03 — Retry, jitter e DLQ

| Cenário | Evidência | Resultado |
|---|---|---|
| Rede, timeout e cancelamento | `TestExecuteClassifiesNetworkAndTimeout` | ✅ |
| HTTP 2xx, redirect, 4xx, 408, 425, 429 e 5xx | `TestExecuteClassifiesHTTPAndRetryAfter` e `TestExecuteTreatsRedirectAsPermanent` | ✅ |
| `Retry-After` em segundos/data | Valores válidos respeitados dentro do teto | ✅ |
| `Retry-After` inválido, overflow ou acima do teto | Ignorado e substituído pelo full jitter determinístico | ✅ |
| Full jitter e cap | `TestRetryPolicyUsesDeterministicFullJitterAndCap` | ✅ |
| Poll vazio com backoff exponencial e jitter limitado | `TestPollPolicyUsesBoundedDeterministicBackoff` | ✅ |
| DLQ e histórico íntegro | Dez retries resultam em `dead_letter` com exatamente dez attempts ordenados | ✅ |
| Status hostil 600–999 | Teste unitário cobre 600, 699 e 999; integração converte 699 em falha permanente sanitizada | ✅ |
| Sem DoS cross-tenant após status hostil | `TestHostileHTTPStatusDoesNotBreakFinalizeOrNextTenant` prova progresso do tenant seguinte | ✅ |

### S2-04 — Graceful shutdown

| Cenário | Evidência | Resultado |
|---|---|---|
| Para novos claims após cancelamento | `TestSchedulerCancellationEscapesBlockedClaim` | ✅ |
| Drena job cooperativo | `TestSchedulerDrainsBeforeShutdown` | ✅ |
| Cancela no fim da graça | `TestSchedulerCancelsJobsAtGraceDeadline` | ✅ |
| Deadline absoluto mesmo com job não cooperativo | `TestSchedulerHardDeadlineWithNonCooperativeJob`, orçamento de 100 ms e limite observado <150 ms | ✅ |
| Pânico em claim/job vira erro controlado | `TestSchedulerReturnsClaimAndJobPanicsAsErrors` | ✅ |
| `SIGTERM` com job ativo | `TestSchedulerSIGTERMWithActiveNonCooperativeJob` e `TestWorkerProcessSIGTERM` | ✅ |
| Limite contratual ≤30 s | Configuração valida o teto e os testes com deadlines reduzidos encerram muito abaixo dele | ✅ |
| Crash recuperável | Lease/fencing e `TestRepeatedCrashesStopAtMaximumAttempts` | ✅ |

## Banco, migrations e segurança

| Verificação | Resultado |
|---|---|
| Migrations do zero `000001`–`000006` | ✅ Versão Goose 6 |
| Schema lógico | ✅ Versão 3 |
| Boundaries `up-to 3/4/5/6` | ✅ Contratos/grants esperados em cada etapa |
| Downgrade `down-to 5/4/3/2` | ✅ Runtime v3 falha fechado em cada boundary incompatível |
| Re-upgrade após `down-to 2` | ✅ `up-to 6` restaura somente o contrato v3 completo |
| Funções privilegiadas | ✅ Owner `wde_worker_executor`, `SECURITY DEFINER`, `search_path=pg_catalog`, sem `PUBLIC EXECUTE` |
| Funções de staging | ✅ Sem `EXECUTE` por runtime; existem apenas nos boundaries esperados |
| Sequence de fairness | ✅ Owner `wde_worker_executor`; executor tem `USAGE`; `wde_worker`, API e admin não têm acesso direto |
| RLS/ACLs | ✅ Zero linhas sem tenant; workspace suspenso não lê/escreve; DML direto em attempts recusado |
| Claim incompleto | ✅ Snapshot incompleto não altera delivery nem attempt |
| Recursos temporários | ✅ Zero bancos `wde_boundary_*`/`wde_bootstrap_*` remanescentes |

Consulta independente ao final retornou:

```text
PostgreSQL=17.11 | schema_logico=3 | goose=6 | sequence_owner=wde_worker_executor | executor_usage=true | worker_usage=false
```

## Regressão da Sprint 1

- Bootstrap/revogação passou em banco temporário isolado e repetível.
- RLS, append-only, idempotência concorrente e snapshot completo passaram.
- O E2E legado `endpoint → event → worker → Chaos Lab → succeeded` passou sem falhas.
- HMAC, auth, endpoint, evento, problem details, logging, configuração e runtime permaneceram verdes.

## Falha de QA encontrada e corrigida

Durante a regressão sobre banco reutilizado, `TestCredentialBootstrapIsSerializedAndRevocable` encontrou 385 workspaces deixados pelos stresses e falhou porque pressupunha banco global vazio. Era um defeito de isolamento do teste, não do runtime. O harness passou a criar, migrar e remover um database exclusivo por execução. O teste passou duas vezes consecutivas e o `make integration` completo passou sobre o banco já usado.

Também foram ampliadas duas evidências sem alterar produção ou migrations:

- o teste de boundary agora executa `down-to 2 → up-to 6` e revalida o fail-closed;
- o teste de status HTTP hostil agora cobre os limites 600 e 999, além de 699.

## Testes práticos

| Teste | Método | Resultado |
|---|---|---|
| Build | `make check` / target `build` | ✅ |
| PostgreSQL real | Container local `postgres:17` em `tmpfs` | ✅ |
| Migrations | `make migrate-up`, `make migrate-status` | ✅ |
| Integração com roles reais | `make integration` | ✅ 17/17 |
| Regressão completa | `go test -count=1 -json ./...` com DSNs reais | ✅ 121/121 |
| Compose | `make compose-config` | ✅ |
| Diff | `git diff --check` | ✅ |

## Pendências

Nenhuma falha funcional, de concorrência, migration, segurança ou regressão permanece aberta para a Sprint 2.

Por restrição explícita deste QA, imagens da aplicação não foram construídas e nenhum pull foi realizado. O contrato Compose foi validado estaticamente e os binários locais foram compilados/exercitados contra PostgreSQL real.

## Veredicto

**QA Sprint 2 aprovado.** `S2-01`, `S2-02`, `S2-03` e `S2-04` atendem aos critérios de aceite testados. A branch está apta a seguir para consolidação do gate de Code Review e integração conforme o pipeline do projeto.
