# Backlog do MVP: Webhook Delivery Engine

**Status:** Pronto para execução incremental  
**Versão:** 1.0  
**Data:** 2026-09-23

## 1. Regras de execução

- Cada sprint termina com comportamento executável e teste automatizado.
- Nenhuma task é concluída somente porque o código compila; seu DoD precisa estar atendido.
- Alterações em tenancy, HMAC, SSRF, retenção, lease/fencing ou semântica de entrega exigem ADR e revisão de segurança.
- DDL/funções privilegiadas recebem review específico antes do release.
- Dashboard, Kafka, Redis e Kubernetes permanecem fora do MVP.
- Complexidade: `S` até 1 dia focado; `M` 1–3 dias; `L` deve ser decomposta antes de implementação.

## 2. Gates globais

| Gate | Evidência |
|---|---|
| G-01 Qualidade | `gofmt`, `go test ./...`, `go vet`, Staticcheck e `govulncheck` passam |
| G-02 Concorrência | `go test -race ./...` passa e não há goroutine fire-and-forget |
| G-03 Segurança | testes de tenant, SSRF, HMAC, limits e canários passam |
| G-04 Banco | migrations do zero e upgrade passam com roles reais/RLS |
| G-05 Operação | Compose, health/readiness e shutdown são reproduzíveis |
| G-06 Evidência | README, OpenAPI, ADRs e resultados não prometem `exactly-once` |

## 3. Sprint 0 — Fundação reproduzível

### S0-01 — Inicializar módulo e estrutura

- **Complexidade:** S
- **Referências:** architecture §§4–7; ADR-001; ADR-005; `AGENTS.md`.
- **Escopo:** `go.mod`, `cmd/api`, `cmd/worker`, `cmd/chaoslab`, `internal/*`, `api`, `migrations`, `test`.
- **DoD:** Go 1.27 fixado; três binários compilam; estrutura respeita dependências; não existem `utils/helpers/common`.

### S0-02 — Configuração tipada e startup/shutdown

- **Complexidade:** M
- **Referências:** architecture §§7, 18–19; `SEC-022`; US-012.
- **DoD:** profiles `local/test/production`; validação fail-closed; `context` e sinais coordenados; testes de configuração inválida; nenhum segredo é logado.

### S0-03 — Logger e erros base

- **Complexidade:** S
- **Referências:** architecture §§19–20; design system §3; `SEC-020`.
- **DoD:** `slog` JSON; request ID; problem details estáveis; allowlist de campos; teste canário inicial.

### S0-04 — PostgreSQL local e migrations

- **Complexidade:** M
- **Referências:** data architecture §§8–11; security schema §5; ADR-002/005.
- **DoD:** Compose inicia PostgreSQL 17; migrator explícito; runtime não migra; migration mínima fail-closed; healthcheck sem credencial; dados em volume nomeado.

### S0-05 — CI e ferramentas pinadas

- **Complexidade:** M
- **Referências:** architecture §§21–22; `SEC-023`/`SEC-024`.
- **DoD:** tools no `go.mod`; jobs para format/test/vet/staticcheck/govulncheck/race; cache não contém segredo; README explica execução local.

## 4. Sprint 1 — Primeiro corte vertical

### S1-01 — Schema mínimo tenant-safe

- **Complexidade:** M
- **Referências:** data architecture §§4–9; `SCHEMA-SEC-001/005/008/009`.
- **Escopo:** workspaces, API keys/scopes, endpoints/secrets/subscriptions, events, deliveries e attempts; roles/RLS junto da criação.
- **DoD:** migrations up/down local; FKs compostas; RLS fail-closed; roles reais; testes provam zero linhas sem tenant e DML proibido em histórico.

### S1-02 — Bootstrap local de workspace e API key

- **Complexidade:** M
- **Referências:** architecture §§8–9; `SEC-001`–`SEC-004`; US-001.
- **DoD:** comando one-shot sem listener; chave CSPRNG exibida uma vez; banco guarda prefixo+verifier; escopos mínimos; autenticação com dummy verifier; revogação testada.

### S1-03 — Criar e consultar endpoint

- **Complexidade:** M
- **Referências:** PRD `FR-002`–`FR-004`; US-002/003; design §§2–3.
- **DoD:** `POST/GET /v1/endpoints`; tenant/scopes; URL local permitida somente em profile local; segredo HMAC exibido uma vez e cifrado; problem details testados.

### S1-04 — Publicar evento idempotente

- **Complexidade:** M
- **Referências:** PRD `FR-005`–`FR-007`; US-004; data architecture §5.
- **DoD:** `POST /v1/events`; limite 1 MiB; evento+deliveries no mesmo commit; 202 após commit; repetição retorna mesmo ID; conteúdo diferente retorna 409; teste concorrente inicial.

### S1-05 — Worker de concorrência 1

- **Complexidade:** M
- **Referências:** architecture §§10.2–10.4; data architecture §6; ADR-003/004.
- **DoD:** claim transacional; HTTP fora da transação; tentativa registrada; `2xx` finaliza; falha fica visível; cancelamento e deadlines propagados.

### S1-06 — HMAC v1

- **Complexidade:** M
- **Referências:** architecture §12; ADR-006; `SEC-005`–`SEC-007`; US-005.
- **DoD:** bytes canônicos; base64url; headers versionados; comparação constante no receptor; vetores de teste independentes; alteração de qualquer campo invalida assinatura.

### S1-07 — Chaos Lab e consulta de delivery

- **Complexidade:** M
- **Referências:** design §5; US-010/014; `SEC-021`.
- **DoD:** Chaos Lab separado e local; cenário success; `GET /v1/deliveries/{id}` com timeline sem payload/segredo; teste E2E publica e observa `succeeded`.

## 5. Sprint 2 — Confiabilidade e concorrência

### S2-01 — Claim em batch com fairness

- **Complexidade:** M
- **Referências:** data architecture §§6, 10.2; `SCHEMA-SEC-007/010`; US-006/008.
- **DoD:** `SKIP LOCKED`; batch ≤ capacidade; limite por workspace/endpoint; teste com múltiplos workers e tenant saturado.

### S2-02 — Lease, fencing e recuperação

- **Complexidade:** M
- **Referências:** ADR-004; data architecture §6; `NFR-002/003`.
- **DoD:** token monotônico; finalização obsoleta afeta zero linhas; attempt antigo vira abandoned; recuperação ≤ TTL+5 s; nenhum `max+1`.

### S2-03 — Retry, jitter e DLQ

- **Complexidade:** M
- **Referências:** PRD `FR-012`–`FR-014`; US-007/009.
- **DoD:** classificação HTTP/rede; full jitter determinístico em teste; `Retry-After` limitado; DLQ após máximo; histórico íntegro.

### S2-04 — Graceful shutdown

- **Complexidade:** M
- **Referências:** architecture §10.6; US-012; `FR-017`.
- **DoD:** para claims, espera jobs, cancela no prazo, encerra ≤30 s; crash deixa trabalho recuperável; testes com sinal/processo.

## 6. Sprint 3 — Tenancy, replay e operação

### S3-01 — RLS e funções privilegiadas completas

- **Complexidade:** M
- **Referências:** data architecture §§8–9; `SCHEMA-SEC-001/008/009`.
- **DoD:** matriz de ACL materializada; search path fixo; sem SQL dinâmico; testes de pool/commit/rollback/panic; review de segurança das funções aprovado.

### S3-02 — Quotas e limites persistentes

- **Complexidade:** M
- **Referências:** architecture §16; `SEC-004`, `SEC-016/017`.
- **DoD:** limites globais/tenant/chave; fan-out/página limitados; buckets expiram; reinício não burla quota; nenhum label de alta cardinalidade.

### S3-03 — Replay por geração

- **Complexidade:** M
- **Referências:** data architecture §7; `SCHEMA-SEC-002`; US-011.
- **DoD:** novo run; sequência histórica monotônica; idempotência/fingerprint versionado; comando+audit+transição atômicos; payload purgado bloqueia.

### S3-04 — Auditoria append-only

- **Complexidade:** S
- **Referências:** `SEC-019`; data architecture §§4.10, 9.
- **DoD:** ações sensíveis auditadas; sem update/delete runtime; snapshots sobrevivem purge; canários ausentes.

## 7. Sprint 4 — Segurança de saída e dados

### S4-01 — Cliente HTTP anti-SSRF

- **Complexidade:** L → decompor parser/policy, resolver/dialer e TLS/tests.
- **Referências:** architecture §13; ADR-007; `SEC-012`–`SEC-015`.
- **DoD:** todos A/AAAA validados; IP pinado; SNI original; proxies/redirects off; IPv4/IPv6/IDN/rebinding testados; headers/body/tempo limitados.

### S4-02 — Envelope AES-256-GCM

- **Complexidade:** M
- **Referências:** architecture §14; data architecture §§2, 4; `SCHEMA-SEC-006`.
- **DoD:** AAD canônico; nonce novo; key version; all-or-none; adulteração falha; plaintext e key material não aparecem em sinks.

### S4-03 — Rotação HMAC

- **Complexidade:** M
- **Referências:** ADR-006; `SEC-007`; US-003.
- **DoD:** active/retiring; assinatura dupla; janela limitada; concorrência segura; segredo antigo purgado depois da janela.

### S4-04 — Retenção, purge e restore quarantine

- **Complexidade:** M
- **Referências:** data architecture §10; `SCHEMA-SEC-003/004`; `SEC-010/011`.
- **DoD:** purge idempotente; fencing invalida worker; payload_expired terminaliza; restore revoga todas as chaves e desabilita tráfego antes da readiness; teste de backup anterior à revogação.

## 8. Sprint 5 — Observabilidade e demonstração

### S5-01 — Métricas e traces

- **Complexidade:** M
- **Referências:** architecture §20; ADR-008; US-013.
- **DoD:** Prometheus/OTel; métricas de fila/tentativa/latência; labels bounded; traces correlacionados; exportador falha sem quebrar negócio.

### S5-02 — Probes e superfície operacional

- **Complexidade:** S
- **Referências:** architecture §20.4; `SEC-021`.
- **DoD:** listener interno; liveness/readiness mínimas; migrations/keyrings/purge considerados; pprof ausente em produção.

### S5-03 — Chaos Lab completo

- **Complexidade:** M
- **Referências:** design §5; US-014.
- **DoD:** success, fail-N, timeout, 429, permanent failure e verify-signature; cenários determinísticos e documentados.

### S5-04 — OpenAPI e quickstart

- **Complexidade:** M
- **Referências:** design §§2–4; `FR-021`; US-015.
- **DoD:** contrato lintado; exemplos curl; consumidor HMAC; jornada ≤10 min; nenhuma credencial real.

## 9. Sprint 6 — Hardening e release de portfólio

### S6-01 — Suite adversarial final

- **Complexidade:** M
- **Referências:** architecture §21; security reviews.
- **DoD:** tenant matrix, SSRF, race, leaks, limites, crash e migrations passam; security review de DDL/funções aprovado.

### S6-02 — Benchmarks e profiling

- **Complexidade:** M
- **Referências:** PRD `NFR-004/005`; US-015.
- **DoD:** ≥100 ingestões/s p95<200 ms e ≥100 deliveries/s medidos ou divergência documentada; hardware/dados/configuração publicados; teste prolongado sem crescimento contínuo.

### S6-03 — Containers, SBOM e scan

- **Complexidade:** M
- **Referências:** `SEC-023/024`; architecture §§22–23.
- **DoD:** imagens mínimas/non-root/read-only; SBOM; scan da imagem exata; Compose smoke; nenhum High/Critical sem exceção válida.

### S6-04 — Runbook e demonstração

- **Complexidade:** M
- **Referências:** `SEC-025`; PRD métricas de portfólio.
- **DoD:** incidentes de key/HMAC/SSRF/payload/dependência; demo retry/restart/DLQ/tracing; limitações e trade-offs explícitos; tag `v1.0.0` somente após todos os gates.

## 10. Definition of Done do MVP

- Histórias P0 e P1 do documento de user stories atendidas.
- Zero evento confirmado perdido nos testes de crash.
- Zero finalização aceita com fencing obsoleto.
- Zero acesso cross-tenant na matriz de rotas.
- Race detector e scanners passam.
- Security Reviews de migrations, funções privilegiadas e release aprovados.
- Quickstart, Chaos Lab e benchmarks são reproduzíveis.
- Toda limitação conhecida está documentada honestamente.

