# Console Web e Observabilidade — plano de entrega

**Status:** Em execução  
**Versão:** 1.0  
**Data:** 2026-09-25

## Resultado do primeiro release

O usuário conecta uma API key existente, cadastra um endpoint, publica um evento, acompanha a delivery e suas tentativas, solicita replay e visualiza métricas agregadas do próprio workspace. O operador acompanha runtime, fila e traces por uma stack privada.

```text
API key → sessão curta → endpoint → evento → delivery/tentativas → replay/DLQ → painel
```

Ficam fora: signup, equipes, recuperação de senha, billing, edição/pausa/delete de endpoint, rotação pela UI, alertas, Loki, WebSocket, HA, Grafana público e traces para clientes.

## Superfícies

| Público | Uso | Autenticação |
|---|---|---|
| API | integração de sistema | Bearer API key |
| Console | jornada humana SSR | cookie de sessão derivada |
| Showcase/Swagger | apresentação/documentação | sem dados privados |
| Grafana | operação | conta de operador via túnel |

Prometheus, Tempo, Collector e listeners operacionais não são públicos.

## Backlog executável

### C1 — Fundação de segurança

- Roles `wde_console` e executor `NOLOGIN`, secrets e passfile separados.
- Migration v6 com sessões, create/lookup/resume/revoke/purge e índices.
- `cmd/console`, login/logout, cookie `__Host-`, CSRF, headers, timeouts e rate limit.
- Aceite: API key não é persistida; scopes permanecem vivos; idle 15m/absolute 60m; role real, RLS, timing/dummy verifier e cross-tenant testados.

### C2 — Leitura útil

- Shell SSR responsivo, lista de endpoints, deliveries e detalhe/timeline.
- Keyset/cursor opaco, estados vazios e erros seguros.
- Aceite: `deliveries:read`, sem N+1, CSP/no-store, escaping e browser smoke.

### C3 — Fluxo demonstrável

- Criar endpoint, publicar evento e replay usando os serviços existentes.
- Segredo exibido uma vez; idempotência; nenhuma chamada direta ao destino pelo console.
- Aceite: E2E endpoint → event → worker → timeline/retry/DLQ, SSRF, quotas e canários.

### C4 — Observabilidade do produto

- Resumo e série 24h, fila, outcomes e p95 tenant-scoped.
- Atualização same-origin a cada 10–30 segundos.
- Aceite: isolamento entre tenants, no máximo 25 pontos, timeout de 2s, query p95 abaixo de 500ms na massa de referência.

### C5 — Observabilidade operacional

- Collector com `memory_limiter`/batch, Prometheus, Tempo e Grafana provisionados.
- Rede `internal`, retenção e recursos limitados; OTLP HTTP somente no hostname privado fixo.
- Aceite: targets ativos, trace pesquisável, dashboards úteis, queda da stack sem impacto e zero porta observacional pública.

### C6 — Hardening e publicação

- Revisão de código, QA, security audit, CI, docs, runbooks, link na vitrine e deploy EasyPanel.
- Aceite: migrations fresh/upgrade/rollback, smoke desktop/mobile, SBOM/scans, soak e rollback documentado.

## Gates de release

- Cookie, CSRF, revogação, expiração e session fixation testados.
- API continua recusando cookie sem Bearer.
- Role real/RLS provam isolamento em todas as novas consultas e mutações.
- Nenhum token, key, segredo, payload, URL completa ou corpo externo aparece em sinks.
- Stack operacional indisponível não afeta o motor.
- `go test`, integração PostgreSQL, race, vet, Staticcheck, `govulncheck`, secret/image scan e SBOM passam.
