# Changelog

Todas as mudanças relevantes deste projeto são documentadas neste arquivo a partir
dos commits convencionais do repositório.

## [Unreleased]

## [1.2.0] — 2026-09-25

### Adicionado

- Console web Go SSR/BFF responsivo com sessão derivada de API key, listagem de
  endpoints e deliveries, timeline de tentativas, criação de endpoint, publicação de
  evento, replay e métricas do próprio workspace.
- Migration lógica v6 com sessões opacas, expiração idle/absolute, revogação,
  auditoria, purge bounded, role PostgreSQL exclusiva e índices de fila/métricas.
- Observabilidade operacional privada com OpenTelemetry Collector, Prometheus, Tempo
  e Grafana provisionados como código, incluindo dois dashboards e canário de trace.
- Smoke Playwright/Axe em Chromium desktop/mobile, com evidências de layout,
  acessibilidade, login, cookie e logout.

### Segurança

- Cookie `__Host-`, CSP estrita, CSRF vinculado à sessão, validação de `Origin`, rate
  limit antes do lookup, scopes vivos, máximo de cinco sessões e isolamento por RLS.
- Grafana limitado ao loopback/túnel SSH; Prometheus, Tempo, Collector e listeners
  operacionais permanecem sem domínio ou porta pública.
- Artefatos do browser smoke são sanitizados, validados com canário e enviados
  somente após sucesso; trace, vídeo e screenshot automático do login ficam
  desligados.
- Supply chain ampliada para as imagens pinadas de Collector, Prometheus, Tempo e
  Grafana, com SBOM e bloqueio de vulnerabilidades High/Critical.

### Corrigido

- Logout agora falha fechado, não consome quota de endpoint e está disponível também
  na navegação mobile.
- `Referrer-Policy` compatível com a defesa de CSRF em formulários reais e atualização
  de métricas pausável no lugar do meta-refresh inacessível.
- Métrica de fila atual inclui itens antigos, purge respeita limite físico total e o
  downgrade da migration serializa escritores concorrentes.
- Dashboards do worker usam séries corretamente escopadas e links externos da vitrine
  abrem em nova página sem desalinhamento do terminal.

### Validação

- Code Review final aprovado com zero blocker, warning ou suggestion.
- QA aprovado com 244 testes, race detector, stress dos pacotes críticos, integração
  PostgreSQL 17, Playwright/Axe desktop/mobile e p95 de `19,76 ms` para consulta com
  25.000 tentativas.
- Security Audit OWASP A01–A10 aprovado para CI de release; `govulncheck`, `npm audit`
  e Gitleaks sem achados.

## [1.1.0] — 2026-09-25

### Adicionado

- Vitrine pública em pt-BR, Swagger UI e topologia de demonstração para EasyPanel.
- Gerador local de segredos fortes e smoke de CI para a topologia produtiva completa.

### Segurança

- PostgreSQL sem porta TCP no host único, acessado por socket Unix explicitamente
  declarado, com SCRAM, roles mínimas e credenciais montadas como arquivos `0400`.
- API, worker e vitrine continuam non-root, read-only, sem capabilities e com limites
  conservadores adequados à VPS de demonstração.

## [1.0.1] — 2026-09-24

### Corrigido

- O scheduler do worker agora mantém o processo vivo durante indisponibilidades
  transitórias do PostgreSQL, sinaliza indisponibilidade por readiness e retoma o
  processamento com backoff sem reiniciar o container.
- Panics em claim ou finalização continuam fatais, evitando mascarar defeitos de
  programação; falhas operacionais do store possuem cobertura de regressão.

### Infraestrutura

- Actions oficiais de checkout, setup do Go e upload de artefatos atualizadas para
  runtimes Node.js atuais, mantendo todas as referências pinadas por SHA imutável.
- Pipeline público validado com unit tests, race detector, análise estática, scan de
  segredos, matriz adversarial, perda do banco, fail-closed, SBOM e scan de imagens.

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
