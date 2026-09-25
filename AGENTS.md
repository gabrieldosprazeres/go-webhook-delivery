# AGENTS.md — Webhook Delivery Engine

> Apenas regras específicas deste projeto. As instruções globais do workspace continuam aplicáveis.

## Stack

- Go 1.27.x, PostgreSQL 17.x, `net/http`, `log/slog`, `pgx/v5`, `sqlc`, migrations versionadas, OpenTelemetry/Prometheus e Testcontainers.
- Monólito modular com binários `api`, `worker`, `console` e `chaoslab`, mais os servidores estáticos `showcase` e `docs`; o console é SSR/BFF em Go, sem framework SPA, Redis, Kafka ou Kubernetes.

## Regras de negócio permanentes

- Semântica pública é `at-least-once`; nunca alegar `exactly-once`. `event_id` e `delivery_id` permanecem estáveis entre tentativas.
- `202 Accepted` somente após commit atômico do evento e de todas as entregas elegíveis.
- `workspace_id` sempre deriva da API key e integra toda consulta/transição; nunca aceitar tenant informado pelo cliente como autoridade.
- Toda finalização de delivery exige lease owner, fencing token vigente e lease não expirado. HTTP externo nunca ocorre dentro de transação.
- Payloads, API keys, HMAC secrets, assinaturas, headers e corpos externos nunca entram em logs, traces, métricas, auditoria ou erros.
- Destinos são hostis: HTTPS, validação SSRF em cada conexão, IP validado fixado no `DialContext`, proxy/redirect desabilitados e TLS verificado.
- Segredos HMAC e payloads ficam cifrados no nível da aplicação; replay é recusado depois do purge do payload.
- Chaos Lab e profiling não podem integrar a superfície/imagem produtiva.
- A vitrine `showcase` é somente apresentação: não acessa banco, segredos ou regras de negócio.
- `docs` serve o OpenAPI e assets Swagger pinados, sem persistir autorização no navegador; não acessa banco ou secrets.
- A API key apresentada no login do console existe somente durante a requisição; nunca entra em cookie, sessão, HTML, URL, storage, log, trace ou métrica.
- Sessões do console usam token opaco, verificador HMAC no PostgreSQL, 15 minutos de inatividade e 60 minutos absolutos; scopes são relidos da API key.
- Toda mutação do console exige scope, `Origin` same-origin e CSRF vinculado à sessão.
- Observabilidade do cliente é sempre agregada e tenant-scoped via PostgreSQL. Nunca adicionar tenant ou resource ID como label Prometheus.
- Prometheus, Tempo, OTel Collector e listeners operacionais não recebem ingress público. Grafana é somente operacional e autenticado.
- OTLP HTTP em produção só é permitido com opt-in para `http://otel-collector:4318` na rede Docker interna dedicada.

## Regras técnicas do projeto

- Pacotes por capacidade; não criar `utils`, `helpers`, `common`, repository genérico, ORM, service locator, mutable globals ou goroutine fire-and-forget.
- Interfaces pequenas são definidas pelo consumidor. Handlers não importam `postgres/sqlc`; composition roots ficam em `cmd/*`.
- Ferramentas são pinadas no `go.mod` com `go get -tool`; arquivos gerados por `sqlc` não são editados manualmente.
- Configuração produtiva inválida ou sem key material falha no startup; segredo real, `.env` e certificado privado não são versionados.
- Toda mudança em protocolo HMAC, estados, retenção, SSRF, tenancy, lease/fencing ou semântica de entrega exige ADR e revisão de segurança.
- CI deve passar testes, integração, `-race`, vet, Staticcheck, `govulncheck`, scanner de segredo/imagem e SBOM antes de release.

## Documentação do projeto

- `docs/webhook-delivery-engine-prd.md` — produto, escopo e requisitos.
- `docs/webhook-delivery-engine-user-stories.md` — histórias e critérios de aceite.
- `docs/webhook-delivery-engine-design-system.md` — contrato de experiência API-first.
- `docs/webhook-delivery-engine-security-review-prd.md` — baseline `SEC-001` a `SEC-025`.
- `docs/webhook-delivery-engine-architecture.md` — arquitetura, fluxos, segurança e evolução.
- `docs/adr/` — decisões arquiteturais e alternativas.
- `docs/webhook-delivery-engine-data-architecture.md` — schema e migrations (próxima etapa).
- `docs/webhook-delivery-engine-backlog.md` — sprints e tasks (próxima etapa).
- `docs/webhook-delivery-engine-status.md` — progresso do pipeline (quando criado).
- `docs/easypanel-deployment.md` — topologia e runbook da demonstração pública.
- `docs/console-observability-plan.md` — escopo, contrato, backlog e gates do console/observabilidade.
- `docs/webhook-delivery-engine-security-audit-easypanel.md` — gate de segurança do deploy.
