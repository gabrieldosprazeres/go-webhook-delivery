# Webhook Delivery Engine

Servico de entrega confiavel de webhooks escrito em Go. O projeto demonstra ingestao idempotente, entrega `at-least-once`, retries, leases com fencing, isolamento multi-tenant, assinatura HMAC, defesa SSRF e operacao observavel.

> Estado atual: Sprint 3 implementada. Alem da entrega concorrente confiavel, a API aplica quotas PostgreSQL globais/tenant/chave, pagina consultas, cria replay por geracao e registra acoes sensiveis em auditoria append-only. O projeto nunca promete `exactly-once`.

## Stack

- Go 1.27.1, `net/http` e `log/slog`;
- PostgreSQL 17 como fonte de verdade e fila duravel;
- Goose para migrations explicitas;
- `go test`, race detector, vet, Staticcheck e `govulncheck`;
- Docker Compose para o ambiente local.

As ferramentas Go ficam pinadas no `go.mod` e sao executadas com `go tool`.

## Pre-requisitos

- Go 1.27.1;
- Docker 29+ com Docker Compose;
- GNU Make e `rg` (ripgrep).

## Inicio rapido

```bash
cp .env.example .env
docker compose --profile core up --build
```

O profile `core` inicia PostgreSQL 17, executa a migration em um job separado e somente depois inicia API e worker. Os processos de runtime nunca executam migrations.

Em outro terminal:

```bash
curl -i http://127.0.0.1:9090/healthz
curl -i http://127.0.0.1:9090/readyz
```

Para incluir o Chaos Lab isolado:

```bash
docker compose --profile demo up --build
curl -i http://127.0.0.1:8081/healthz
```

Todos os ports publicados pelo Compose fazem bind em loopback. As credenciais presentes em `.env.example` sao exclusivamente locais e descartaveis.

Antes da primeira chamada, crie o workspace e a API key com o comando one-shot (ele nao inicia listener e revela a chave uma unica vez):

```bash
WDE_PROFILE=local \
WDE_ADMIN_DATABASE_URL='postgres://wde_admin:admin-local-only@127.0.0.1:5432/wde?sslmode=disable' \
WDE_DATABASE_URL='postgres://wde_api:api-local-only@127.0.0.1:5432/wde?sslmode=disable' \
go run ./cmd/api credentials bootstrap --name 'Demo local' \
  --output-file='./local-credentials.json'
```

O arquivo criado contém `api_key`. O fluxo HTTP inicial esta documentado em [`api/openapi.yaml`](api/openapi.yaml). Endpoints HTTP sao aceitos apenas nos profiles locais/de teste e apenas em loopback; a criacao permanece fechada em producao ate a defesa SSRF completa da Sprint 4.

## Execucao sem containers

```bash
export WDE_PROFILE=local
export WDE_DATABASE_URL='postgres://wde_api:api-local-only@127.0.0.1:5432/wde?sslmode=disable'
go run ./cmd/api
```

O worker usa a credencial `wde_worker`. O Chaos Lab nao recebe DSN e falha no startup se for iniciado com profile `production`.
Por padrao, o Chaos Lab escuta somente em `127.0.0.1:8081`; no container local o Compose faz override do listener e publica o port exclusivamente em loopback.

## Configuracao

Variaveis nao secretas podem ser substituidas por flags homonimas, como `--profile`, `--http-addr`, `--log-level` e `--shutdown-timeout`. Segredos e DSNs nao possuem flag para evitar exposicao no historico/process list.

Principais variaveis:

| Variavel | Uso |
|---|---|
| `WDE_PROFILE` | `local`, `test` ou `production` |
| `WDE_DATABASE_URL` | DSN da role especifica do processo |
| `WDE_ADMIN_DATABASE_URL` | DSN usada exclusivamente pelos comandos one-shot de credenciais |
| `WDE_DATABASE_TIMEOUT` | prazo para startup/readiness do banco; default `5s` |
| `WDE_API_HTTP_ADDR` | listener da API; default `:8080` |
| `WDE_API_OPERATIONAL_ADDR` | probes da API; default `127.0.0.1:9090` |
| `WDE_WORKER_OPERATIONAL_ADDR` | probes do worker; default `127.0.0.1:9091` |
| `WDE_CHAOSLAB_HTTP_ADDR` | listener local; default `127.0.0.1:8081` |
| `WDE_ALLOW_HTTP_DESTINATIONS` | habilita explicitamente destinos HTTP loopback fora de produção |
| `WDE_EDGE_{MAX_IN_FLIGHT,GLOBAL,ORIGIN,PREFIX,WINDOW,MAX_BUCKETS}` | semáforo e limiter local bounded antes do lookup de credencial |
| `WDE_QUOTA_<OPERACAO>_{GLOBAL,WORKSPACE,API_KEY}` | limites persistentes por janela para ingestao, escrita de endpoint, consulta e replay |
| `WDE_QUOTA_REPLAY_RESOURCE` | limite de replay por delivery na janela; default `2/min` |
| `WDE_QUOTA_<OPERACAO>_WINDOW` | janela fixa PostgreSQL da quota (`1s` para ingestao; `1m` para as demais) |
| `WDE_WORKER_CONCURRENCY` | jobs HTTP simultaneos; default `8` |
| `WDE_WORKER_CLAIM_BATCH_SIZE` | claims por ciclo, sempre menor ou igual a concorrencia; default `8` |
| `WDE_WORKER_WORKSPACE_BATCH_LIMIT` | teto de claims por workspace em um batch; default `2` |
| `WDE_WORKER_ENDPOINT_BATCH_LIMIT` | teto de claims por endpoint em um batch; default `2` |
| `WDE_WORKER_POLL_INTERVAL` | base do backoff com jitter quando a fila esta vazia; default `250ms` |
| `WDE_WORKER_CLAIM_TIMEOUT` | deadline de cada acesso de claim ao banco; default `200ms`, sempre menor ou igual ao poll e ao lease |
| `WDE_WORKER_REQUEST_TIMEOUT` | timeout HTTP total; default `10s`, maximo `20s` |
| `WDE_WORKER_LEASE_TTL` | validade do lease; default `30s` e margem minima de `10s` sobre o timeout |
| `WDE_WORKER_RETRY_BASE` / `WDE_WORKER_RETRY_CAP` | janela exponencial com full jitter; defaults `1s` / `15m` |
| `WDE_SHUTDOWN_TIMEOUT` | prazo de shutdown; default `30s` |
| `WDE_LOG_LEVEL` | `debug`, `info`, `warn` ou `error` |
| `WDE_INGRESS_TLS_TERMINATED` | declaracao obrigatoria para API em producao |
| `WDE_*_FILE` | paths de peppers e keyrings montados em producao; rate limit e cursor usam peppers independentes |

No profile `production`, o startup falha se o PostgreSQL nao usar `sslmode=verify-full`, TLS de entrada nao estiver declarado, profiling/HTTP externo estiver habilitado ou os secret files estiverem ausentes, forem symlinks, tiverem permissoes acima de `0600` ou pertencerem a outro UID.

O scheduler nunca reserva mais que os slots livres. Uma sequence monotônica, sem row lock global, alimenta cursores persistidos por workspace e endpoint inclusive entre batches unitários e workers concorrentes. Workspace, endpoint runtime e delivery usam locks locais com `SKIP LOCKED`, portanto um tenant travado não impede progresso independente. Cada claim possui deadline de banco explícito. Polling vazio usa backoff exponencial com jitter para evitar sincronização entre instâncias. Um lease expirado abandona a tentativa antiga e incrementa o fencing token antes de novo envio. Resultados com owner/token vencidos afetam zero linhas.

Antes do lookup de credencial, cada instância aplica semáforo global e limiter fixed-window por volume global, origem observada no socket e prefixo apenas sintático. A memória usa LRU com cardinalidade máxima, identificadores HMAC e ignora headers de origem enviados pelo cliente. Depois da autenticação, a quota persistente roda antes da autorização por escopo.

Quotas autenticadas usam buckets PostgreSQL em uma unica transacao para dimensoes global, workspace, API key e, no replay, delivery. O relogio da janela e `transaction_timestamp()`, portanto todas as dimensoes da mesma requisicao compartilham o mesmo boundary. Reiniciar ou multiplicar instancias nao zera contadores; buckets expirados sao removidos em lotes limitados. Identificadores de dimensao incluem versão, operação, janela e tenant/recurso no HMAC com pepper exclusivo e nunca viram labels de métrica.

`POST /v1/deliveries/{id}/replays` exige `deliveries:retry`, motivo, `Idempotency-Key` e quota. Um replay valido cria novo `run_number`, zera somente o contador do run e preserva `attempt_sequence`, fencing e historico. Comandos concorrentes identicos retornam o mesmo comando; conteudo divergente gera `409`; payload expurgado falha fechado. Comando, transicao e auditoria confirmam ou revertem juntos.

Cursores de listagem são binários, versionados, vinculados ao workspace e autenticados com HMAC. Alteração de qualquer byte ou uso em outro tenant retorna `400 invalid_page` sem revelar dados.

Timeouts, erros de rede, `408`, `425`, `429` e `5xx` recebem retry. Redirects, os demais `4xx` e status fora de `100..599` sao falhas permanentes. O atraso usa full jitter exponencial; `Retry-After` so e aceito quando valido e dentro do teto, caso contrario a politica padrao prevalece. A ultima falha transitoria termina em `dead_letter`, preservando todas as tentativas. Em `SIGINT`/`SIGTERM`, o worker para novos claims, drena jobs, cancela os restantes antes do fim e retorna no deadline absoluto de no maximo 30 segundos mesmo diante de dependencia nao cooperativa.

Peppers produtivos usam uma linha `v1:<base64url-sem-padding>` com exatamente 32 bytes aleatorios. Keyrings usam JSON versionado, tambem com chaves de exatamente 32 bytes:

```json
{
  "format_version": 1,
  "primary_key_version": 1,
  "keys": [
    {"version": 1, "material": "<32-bytes-em-base64url-sem-padding>"}
  ]
}
```

Peppers de autenticação, idempotência, fingerprint, dimensões de quota e cursor, assim como os keyrings de payload/assinatura, devem ter materiais distintos. Arquivos vazios, formatos desconhecidos, chaves curtas, versões duplicadas e JSON com campos desconhecidos impedem o startup. A API e o worker também validam conexão, role PostgreSQL e versão da migration.

`/healthz` e `/readyz` existem somente nos listeners operacionais: liveness indica processo vivo; readiness valida banco, role e schema a cada chamada e retorna `503` quando algum deles deixa de ser compativel ou acessivel. A superficie publica da API responde `404` para essas rotas. Os defaults operacionais e do Chaos Lab usam loopback; o Compose faz bind interno para containers e publica as portas somente em `127.0.0.1`.

## Comandos

```bash
make help
make fmt
make test
make integration
make race
make vet
make staticcheck
make vuln
make openapi-lint
make migration-validate
make build
make check
```

Para aplicar migrations fora do Compose:

```bash
export WDE_MIGRATOR_DATABASE_URL='postgres://wde_migrator:migrator-local-only@127.0.0.1:5432/wde?sslmode=disable'
make migrate-up
```

## Estrutura

```text
cmd/                 composition roots de api, worker e chaoslab
internal/auth/       API keys, principal e bootstrap/revogacao
internal/endpoint/   destinos, subscriptions e segredo de assinatura
internal/event/      ingresso idempotente e fan-out transacional
internal/delivery/   claim, envio HTTP e timeline
internal/signing/    protocolo HMAC v1
internal/platform/   configuracao, criptografia, logging e lifecycle
db/migrations/       migrations versionadas e fail-closed
deployments/         Dockerfiles e bootstrap local do PostgreSQL
api/                 contrato OpenAPI
test/                suites de integracao e adversariais
docs/                PRD, arquitetura, ADRs, seguranca, backlog e status
```

## Decisoes e seguranca

- [Arquitetura](docs/webhook-delivery-engine-architecture.md)
- [Arquitetura de dados](docs/webhook-delivery-engine-data-architecture.md)
- [Backlog do MVP](docs/webhook-delivery-engine-backlog.md)
- [ADRs](docs/adr/)
- [Status](docs/webhook-delivery-engine-status.md)

Payloads, API keys, HMAC secrets, assinaturas, headers, URLs completas, DSNs e corpos externos nunca devem entrar em logs, traces, metricas ou erros. O logger da fundacao aplica uma allowlist de atributos e possui teste canario contra vazamento.
