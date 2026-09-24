# Auditoria de segurança — Webhook Delivery Engine

**Data:** 2026-09-24

**Escopo:** candidato da Sprint 6, branch `feature/sprint-6-hardening-release`

**Referência de código:** `5dcab024affb3d72d616a39694bdf76de1872122` mais o diff da Sprint 6

**Veredicto:** ✅ Aprovado para integração e preparação da release `v1.0.0`

## 1. Resumo executivo

Nenhum blocker, warning de segurança, segredo versionado ou vulnerabilidade
High/Critical foi encontrado. O sistema aplica isolamento multi-tenant em banco,
autorização por escopo, criptografia autenticada, defesa SSRF com resolução e conexão
no mesmo IP validado, limites persistentes, logs allowlisted e containers mínimos.

A API pública também aplica, inclusive em `404` e `405`, CSP restritiva,
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer` e `Cache-Control: no-store`. HSTS é emitido somente no
profile `production`, quando o operador declara que o TLS termina no ingress. Os
listeners operacionais permanecem separados da superfície pública e devem ficar em
loopback ou rede privada com ACL.

Riscos residuais aceitos: TLS, firewall de egress, secret manager/KMS e isolamento de
rede dependem da plataforma de deploy; o MVP não oferece HA, multi-região ou
certificação de compliance. O throughput de delivery medido ficou abaixo da meta de
referência e está documentado como limitação de capacidade, não como vulnerabilidade.

## 2. Escopo e método

A revisão cobriu as rotas produtivas, autenticação e autorização, RLS/ACLs e funções
`SECURITY DEFINER`, migrations 1–20, criptografia de payload e segredo, assinatura
HMAC, cliente HTTP de saída, retenção/restore/purge, telemetria, arquivos de
configuração, Dockerfiles/Compose, scripts de supply chain e histórico Git.

Foram executados `make check`, a matriz adversarial sem skip, integração em PostgreSQL
17.11, rehearsal de rollback, quickstart, smoke das quatro imagens, verificação de
manifesto de filesystem e scanners pinados. A revisão de código independente terminou
com zero blockers, zero warnings e zero suggestions; o QA independente foi aprovado
sem ressalvas.

O gate de supply chain usou Syft 1.52.0, Trivy 0.74.0 e Gitleaks 8.30.1, baixados por
assets e checksums pinados. Ele produziu CycloneDX e SPDX para cada imagem e escaneou
os IDs imutáveis, não apenas tags mutáveis.

## 3. Resultado por OWASP Top 10

| Categoria | Resultado | Evidência principal |
|---|---|---|
| A01 — Broken Access Control | Aprovado | RLS forçada, tenant em transação, scopes por rota, ACL mínima e testes cross-tenant nas sete rotas. |
| A02 — Cryptographic Failures | Aprovado | AES-256-GCM com AAD, nonces CSPRNG, keyrings separados, HMAC e comparação constante; produção exige PostgreSQL `verify-full` e TLS de entrada declarado. |
| A03 — Injection | Aprovado | SQL de produção parametrizado, sem SQL dinâmico; parsing estrito e limites de entrada; nenhuma execução dinâmica de comandos em runtime. |
| A04 — Insecure Design | Aprovado | Quotas persistentes, edge limiter bounded, idempotência, leases com fencing, retries limitados, DLQ, retenção e restore quarantine fail-closed. |
| A05 — Security Misconfiguration | Aprovado | Defaults fail-closed, listeners operacionais separados, headers de segurança, profile produtivo bloqueia Chaos Lab/HTTP/pprof e containers non-root/read-only. |
| A06 — Vulnerable Components | Aprovado | `govulncheck` sem vulnerabilidade alcançável; Trivy sem High/Critical; bases, actions e ferramentas fixadas por versão/digest/SHA. |
| A07 — Identification and Authentication Failures | Aprovado | API keys CSPRNG armazenadas por HMAC, verificação em tempo constante, revogação, rotação e limitação antes/depois da autenticação. |
| A08 — Software and Data Integrity Failures | Aprovado | Migrations explícitas, rollback guard transacional, imagens por digest, SBOMs duplos e manifesto exato de conteúdo/metadados. |
| A09 — Security Logging and Monitoring Failures | Aprovado | Logs estruturados allowlisted, auditoria append-only e métricas/traces bounded sem segredo, payload, URL completa ou IDs de tenant. |
| A10 — Server-Side Request Forgery | Aprovado | HTTPS obrigatório em produção, canonicalização de hostname/IDN, bloqueio de endereços especiais, re-resolução por conexão, IP pinado, sem proxy ambiental ou redirect. |

## 4. Evidências reproduzíveis

- `make check`: testes, race detector, vet, Staticcheck, `govulncheck`, OpenAPI,
  migrations e builds aprovados.
- `make adversarial`: árvore integral em PostgreSQL 17.11, sem skip.
- `make integration`: regressão S1–S6 e shutdown do worker aprovados.
- `make rollback-rehearsal`: sete cenários aprovados, incluindo writer concorrente.
- `make quickstart`: sete etapas funcionais aprovadas.
- `make container-smoke`: quatro imagens e políticas hardened aprovadas.
- `make supply-chain`: oito SBOMs, quatro relatórios Trivy com zero
  High/Critical e Gitleaks com zero achado.
- `make secret-scan`: histórico Git completo sem vazamento.

IDs do candidato pós-hardening escaneado:

| Imagem | ID imutável |
|---|---|
| API | `sha256:30d47520bbce15dc51ae8015d5320c05a03e7278051ce0b40a5b1ec2dc832b41` |
| Worker | `sha256:718b4409790e5c74dce227db60f6e0e8e4d97f05bb7713f9f90c194a41db8919` |
| Chaos Lab | `sha256:1e4fc9871b0bdd17de58c381fb0247689a67b710bf3a3808896eed3d4d4bb633` |
| Migrator | `sha256:57af47ae47b0d37c663cff7331dc2be4284d967305236167f9437fe5445ea4a3` |

Os IDs acima são evidência do candidato auditado. A checklist exige novo build e novo
scan se labels OCI, revisão ou qualquer byte mudar antes da tag; os IDs finais devem
ser preservados junto aos SBOMs e relatórios externos à imagem.

## 5. Conclusão e condições de release

O código está aprovado para integração. A tag `v1.0.0` só deve ser criada depois de
um commit final limpo, rebuild com `WDE_VERSION=1.0.0` e `WDE_REVISION` igual a esse
commit, smoke/verificação das quatro imagens finais, SBOMs e Trivy/Gitleaks sobre seus
novos IDs. Imagens devem ser publicadas por digest e a infraestrutura deve cumprir as
premissas de TLS, ACL da superfície operacional, PostgreSQL privado, egress firewall e
gestão externa dos arquivos secretos descritas em `SECURITY.md`.
