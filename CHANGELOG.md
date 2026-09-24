# Changelog

Todas as mudanças relevantes deste projeto são documentadas neste arquivo a partir
dos commits convencionais do repositório.

## [1.0.0] — 2026-09-24

### Adicionado

- Fundação em Go 1.27 e PostgreSQL 17 com configuração tipada, migrations explícitas,
  shutdown gracioso, CI e ferramentas de análise pinadas.
- Fluxo completo de webhook: endpoints, ingestão idempotente, fan-out, worker
  concorrente, assinatura HMAC, retries com jitter, leases/fencing e dead-letter.
- Isolamento multi-tenant com RLS/ACL, scopes, quotas persistentes, replay idempotente
  e auditoria append-only.
- Defesa SSRF, criptografia AES-256-GCM, rotação de segredos, retenção, purge de
  workspace e quarentena pós-restore.
- Métricas Prometheus, traces OpenTelemetry, Chaos Lab, consumidor HMAC de exemplo e
  quickstart reproduzível em sete etapas.
- Suite adversarial, benchmark/soak, rehearsal de rollback, runbooks operacional e de
  demonstração e documentação arquitetural completa.

### Segurança e supply chain

- Quatro imagens distroless pinadas por digest, executadas como non-root, rootfs
  read-only, capabilities removidas e `no-new-privileges`.
- SBOMs CycloneDX/SPDX com Syft, scan High/Critical com Trivy, scanner de segredos com
  Gitleaks e manifesto canônico do filesystem das imagens.
- Headers de segurança na API pública, TLS/segredos fail-closed em produção e política
  de reporte em `SECURITY.md`.

### Validação

- Code Review independente aprovado com zero blocker, warning ou suggestion.
- QA final aprovado sem ressalvas em PostgreSQL 17.11, incluindo integração real,
  concorrência, rollback, quickstart e containers.
- Auditoria OWASP A01–A10 aprovada; `govulncheck` sem vulnerabilidade alcançável,
  Trivy sem High/Critical e Gitleaks sem achados.

### Limitações conhecidas

- A entrega é `at-least-once`; consumidores devem ser idempotentes.
- O benchmark local superou a referência de ingestão, mas mediu 32,77 deliveries/s,
  abaixo da meta exploratória de 100 deliveries/s.
- O MVP não inclui HA, multi-região, painel web, KMS/HSM ou publicação gerenciada.
