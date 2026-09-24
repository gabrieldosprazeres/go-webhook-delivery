# QA Report: Sprint 4 — Segurança de saída e dados

**Data:** 2026-09-24  
**Branch:** `feature/sprint-4-output-data-security`  
**Status:** ✅ **APROVADO**

## Veredito binário

**APROVADO.** Os itens `S4-01` a `S4-04` foram validados sem falha pendente. A regressão integral sem cache em PostgreSQL real executou **180/180 testes e subtestes**, em **20 pacotes testados**, com **zero skip** e **zero falha**. `make integration` passou com regressões S1–S3 e os doze cenários S4. A matriz S4 passou **120/120** em stress, os três fluxos concorrentes de maior risco passaram **60/60** e os canários de auditoria passaram **40/40 sob race detector**.

O Code Review independente fornecido como entrada deste gate encerrou a rodada final com **zero blockers e zero warnings**, após a correção focalizada do overlap de restore.

## Ambiente

| Item | Evidência |
|---|---|
| Go | `go1.27.1 darwin/arm64` |
| PostgreSQL | `17.11 (Debian 17.11-1.pgdg13+2)` |
| Imagem | `postgres:17`, já disponível localmente, digest `sha256:f4c66b820c6f974249089d3d16d86a3698eae11e8746eb6644b2271031e91232` |
| Banco de QA | Container isolado `wde-s4-qa-pg`, `tmpfs` de 768 MiB e porta loopback `55436` |
| Docker | Nenhuma imagem foi baixada ou construída; nenhum volume ou rede adicional foi criado |
| Frontend | Não aplicável: produto API-first, sem UI; a11y, viewport e validação visual não se aplicam |

## Validação estática e gates

| Check | Resultado |
|---|---|
| `make check` após os testes de QA | ✅ Passou |
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
| `make integration` | Bootstrap, 15 regressões S1/S2, 8 cenários S3, 12 cenários S4, pipeline público e `SIGTERM` | ✅ Sem falhas ou skips |
| `go test ./... -count=1 -p=1` com as quatro DSNs reais | Regressão integral sem cache em PostgreSQL real | **180/180**, 20 pacotes, 0 skip ✅ |
| 12 cenários S4 com `-count=10` | Anti-SSRF, criptografia, rotação, retenção, purge e restore | **120/120** ✅ |
| Matriz S4 com `-race` | Corridas de dados na aplicação durante integração | **12/12** ✅ |
| Restore + purge/rotation + rotação com `-count=20` | Overlap, fencing, rewind, idempotência e assinatura dupla | **60/60** ✅ |
| Canários de auditoria com `-count=20 -race` | Rotação e purge sem segredo, payload ou ciphertext em audit | **40/40** ✅ |
| Anti-SSRF unitário com `-count=50 -race` | Parser, classificação, DNS, dial, TLS, redirects e limites | **50/50 suítes** ✅ |
| Criptografia/delivery/endpoint com `-count=20 -race` | AEAD, keyrings, HMAC e contratos HTTP | ✅ Sem falhas |
| Compatibilidade AEAD v1 com `-count=20 -race` | Target, payload e segredos active/retiring legados no mesmo claim | **20/20** ✅ |
| Migration boundary com `-count=3` | `up/down/up`, contrato lógico, grants e fail-closed | **3/3** ✅ |

Não existe threshold formal de cobertura percentual Go nesta Sprint. O gate usa critérios de aceite, integração real, race detector, stress, probes dos binários e controles SQL independentes.

## Cobertura por item da Sprint

### S4-01 — Cliente HTTP anti-SSRF

| Cenário | Evidência | Resultado |
|---|---|---|
| Parser e IDN estritos | Normalização IDNA, remoção de ponto final e rejeição de userinfo, query, fragment, zone ID e IP ambíguo | ✅ |
| Classificação IANA IPv4/IPv6 | Loopback, privado, link-local, multicast, documentation, unallocated e demais faixas especiais bloqueadas | ✅ |
| IPv4 mapped e transições | IPv4-mapped, NAT64 WKP/local prefix, 6to4 e Teredo classificados fail-closed | ✅ |
| DNS misto | Resolução com qualquer endereço proibido invalida todo o destino | ✅ |
| DNS rebinding | Cada nova conexão resolve e revalida; IP validado é fixado no dial | ✅ |
| SNI | Hostname original permanece no TLS mesmo com dial no IP pinado | ✅ |
| Proxy e redirect | Proxy ambiental ignorado e redirects recusados | ✅ |
| TLS | Certificado inválido rejeitado; HTTPS é obrigatório fora do modo local explícito | ✅ |
| Deadlines | DNS, conexão, TLS, headers e tentativa compartilham orçamento; servidor lento é interrompido | ✅ |
| Limites | Header acima de 32 KiB e body acima de 64 KiB são rejeitados | ✅ |
| HTTP local | Permitido somente no profile/flag local explícito | ✅ |

### S4-02 — Envelope AES-256-GCM

| Cenário | Evidência | Resultado |
|---|---|---|
| Novas escritas v2 | Envelope versionado com nonce fresco e AAD tipado/length-prefixed | ✅ |
| Leitura v1 | Claim abre target, payload e segredos active/retiring legados | ✅ 20/20 sob race |
| Binding de metadados | Workspace, tipo de recurso, IDs e campos relevantes autenticados por AAD | ✅ |
| Tamper e versão desconhecida | Ciphertext adulterado e formato desconhecido falham fechados | ✅ |
| Keyrings separados | Transplante de envelope entre payload/signing keyrings é recusado | ✅ |
| All-or-none | Constraints impedem envelope parcial e purge limpa todos os componentes | ✅ |
| Ausência de sinks | Logs e auditoria não contêm plaintext, payload canary ou `ciphertext` | ✅ |

### S4-03 — Rotação HMAC

| Cenário | Evidência | Resultado |
|---|---|---|
| Concorrência e idempotência | 12 chamadas concorrentes geram uma rotação e 11 respostas duplicate | ✅ |
| Estados | Exatamente uma chave `active` e uma `retiring` durante overlap | ✅ |
| Assinatura dupla | Claim captura as duas versões e emite ambas no header; ambas verificam | ✅ |
| Overlap | Intervalo aceito entre 1 hora e 7 dias; fora disso é recusado | ✅ |
| Expiração e purge | Segredo retiring é zerado all-or-none; comando idempotente permanece | ✅ |
| Retry tardio | Repetição após purge retorna expiração/conflito estável sem mutação | ✅ |
| Auditoria | Um evento por comando aceito, sem segredo plaintext ou ciphertext | ✅ |

### S4-04 — Retenção, purge e restore quarantine

| Cenário | Evidência | Resultado |
|---|---|---|
| Todas as categorias | Payloads, metadata, comandos, auditoria e buckets são cobertos | ✅ |
| Batch e cap | `NULL`, zero e valores acima de 1.000 são recusados; teto físico é 1.000 | ✅ |
| Drain e SLA | Backlog com 2.501 registros é drenado por progresso antes de um scan final | ✅ |
| Backlog/oldest | Pais ainda inelegíveis são excluídos; vazio publica idade zero | ✅ |
| Payload purge | Attempt iniciado é abandonado, fencing avança, delivery terminaliza e replay falha fechado | ✅ |
| Purge de workspace | Batches/checkpoint, lease/fencing, cancelamento, tombstone e auditoria validados | ✅ |
| Dependência tardia | Novo filho força rewind; nenhum registro elegível fica órfão | ✅ |
| Overlap com rotação | Advisory serialization mantém invariantes sob concorrência | ✅ 20/20 |
| Restore snapshot | Database isolado restaurado entra em quarantine antes de readiness | ✅ |
| Revogação | API keys/HMAC secrets revogados; workspace/endpoint suspensos; leases invalidados | ✅ |
| Readiness/reconcile | Quarantine retorna `503`; reconcile só libera `200` após nenhuma superfície ativa | ✅ |

## Banco, migrations e segurança

| Verificação | Resultado |
|---|---|
| Migrations do zero `000001`–`000019` | ✅ Goose 19 |
| Schema lógico | ✅ Versão 5 |
| Boundaries 11–18 | ✅ Mantêm contrato lógico anterior e staging sem grants runtime prematuros |
| Boundary 19 | ✅ Publica o contrato lógico v5 atomicamente |
| `up/down/up` | ✅ Reversibilidade e republicação segura confirmadas |
| Roles executoras | ✅ `wde_rotation_executor` e `wde_maintenance_executor` são `NOLOGIN` e sem privilégios administrativos/bypass RLS |
| Funções privilegiadas | ✅ Owner mínimo, `SECURITY DEFINER`, `search_path=pg_catalog`, sem `PUBLIC EXECUTE` |
| Tabelas sensíveis | ✅ `secret_rotation_commands` e `maintenance_jobs` com `ENABLE` + `FORCE RLS` |
| DML direto | ✅ API, worker e admin sem DML direto nas tabelas de comando/manutenção |
| Timeouts | ✅ Funções de purge/backlog fixam `statement_timeout=5s` |
| Recursos temporários | ✅ Zero databases boundary/restore/claim-plan/bootstrap remanescentes antes do cleanup |

Consulta independente final:

```text
schema_logico=5 | goose=19
wde_rotation_executor/wde_maintenance_executor: NOLOGIN, sem superuser/createdb/createrole/replication/bypassrls
rotate_endpoint_secret/purge*/retention_backlog/request_workspace_purge/restore*: SECURITY DEFINER, search_path=pg_catalog, PUBLIC EXECUTE=false
secret_rotation_commands/maintenance_jobs: RLS=true, FORCE RLS=true
API/worker/admin: sem DML direto nas tabelas sensíveis
temporary_databases=0
```

## Regressão S1–S3

- Bootstrap/revogação, RLS/ACL, idempotência concorrente e snapshot completo permaneceram verdes.
- Claim/fairness, locks, lease/fencing, retry/DLQ, status hostil e histórico permaneceram verdes.
- Tenant transaction, quotas, fan-out, cursor tenant-bound/tamper, replay e auditoria permaneceram verdes.
- O pipeline público real preservou a ordem `edge limiter → auth → quota → scope → handler`, incluindo `401 → 429`, `403 → 429` e anti-poisoning cross-tenant.
- O E2E `endpoint → event → worker → Chaos Lab → succeeded` e o shutdown por `SIGTERM` permaneceram verdes.

## Probes reais dos binários

| Teste | Resultado |
|---|---|
| Probes no listener público | `/healthz` retornou `404` ✅ |
| API operacional | Liveness/readiness `200/200` ✅ |
| Worker operacional | Liveness/readiness `200/200` ✅ |
| `api restore quarantine --generation 4242` | Liveness `200`; readiness API/worker `503` ✅ |
| `api restore reconcile --generation 4242` | Readiness API/worker voltou a `200` ✅ |
| Encerramento | API e worker encerraram limpos com sinal, exit code 0 ✅ |

## Falhas de QA encontradas e corrigidas

Nenhum defeito de produção permaneceu aberto. O QA realizou somente ajustes de harness/teste:

1. Dois cenários S4 descartavam pools auxiliares retornados por `reliabilityPools`; eles agora são fechados para impedir acúmulo de health-check goroutines em stress.
2. Foram adicionados canários explícitos que provam que auditorias de rotação e purge não recebem segredo, payload ou ciphertext.
3. Foi adicionado um teste nomeado de compatibilidade v1 cobrindo os quatro envelopes usados pelo worker no mesmo claim.

As alterações não modificam código de produção, migrations nem contrato público.

## Observação de investigação do harness

Uma inicialização exploratória do worker usou deadline de claim artificial de **40 ms** sobre o banco já acumulado por toda a campanha e falhou rápido por deadline, como configurado. Com **500 ms**, ainda abaixo do teto suportado, o mesmo binário passou probes, quarantine/reconcile e shutdown. Não houve reprodução em banco limpo, nos defaults, em `make integration`, na regressão sem cache nem nos stresses; portanto, a evidência classifica o evento como configuração agressiva do probe/harness, não como defeito funcional da Sprint.

## Riscos residuais

- A defesa em aplicação deve ser combinada, no deploy, com egress firewall/proxy controlado e DNS confiável; IP pinning não substitui controles de rede.
- Keyrings locais exercitam o protocolo, mas produção deve usar material entregue por secret manager/KMS e procedimento operacional de rotação/recuperação.
- Retenção e purge concentram coordenação no PostgreSQL; métricas de backlog/oldest, alertas e benchmark de capacidade devem ser fechados na Sprint 5.
- A quarantine é fail-closed por processo/geração; o runbook deve garantir execução após todo restore e reconciliação antes de reabrir tráfego.
- Imagens da aplicação não foram reconstruídas localmente por restrição de armazenamento. Binários foram compilados/exercitados e o contrato Compose foi validado sem downloads/builds.

## Pendências

Nenhuma falha funcional, de segurança, concorrência, migration, probe ou regressão permanece aberta para a Sprint 4.

## Veredito

**QA Sprint 4 aprovado.** `S4-01`, `S4-02`, `S4-03` e `S4-04` atendem aos critérios de aceite. A branch está apta para commit, integração em `main` e início da Sprint 5.
