# Webhook Delivery Engine

Servico de entrega confiavel de webhooks escrito em Go. O projeto demonstra ingestao idempotente, entrega `at-least-once`, retries, leases com fencing, isolamento multi-tenant, assinatura HMAC, defesa SSRF e operacao observavel.

> Estado atual: Sprint 0. A fundacao executavel esta pronta; endpoints de negocio, persistencia do dominio e entrega entram nas proximas sprints. O projeto nunca promete `exactly-once`.

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
| `WDE_DATABASE_TIMEOUT` | prazo para startup/readiness do banco; default `5s` |
| `WDE_API_HTTP_ADDR` | listener da API; default `:8080` |
| `WDE_API_OPERATIONAL_ADDR` | probes da API; default `127.0.0.1:9090` |
| `WDE_WORKER_OPERATIONAL_ADDR` | probes do worker; default `127.0.0.1:9091` |
| `WDE_CHAOSLAB_HTTP_ADDR` | listener local; default `127.0.0.1:8081` |
| `WDE_SHUTDOWN_TIMEOUT` | prazo de shutdown; default `30s` |
| `WDE_LOG_LEVEL` | `debug`, `info`, `warn` ou `error` |
| `WDE_INGRESS_TLS_TERMINATED` | declaracao obrigatoria para API em producao |
| `WDE_*_FILE` | paths de peppers e keyrings montados em producao |

No profile `production`, o startup falha se o PostgreSQL nao usar `sslmode=verify-full`, TLS de entrada nao estiver declarado, profiling/HTTP externo estiver habilitado ou os secret files estiverem ausentes, forem symlinks, tiverem permissoes acima de `0600` ou pertencerem a outro UID.

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

Peppers de autenticacao/idempotencia e keyrings de payload/assinatura devem ter materiais distintos. Arquivos vazios, formatos desconhecidos, chaves curtas, versoes duplicadas e JSON com campos desconhecidos impedem o startup. A API e o worker tambem validam conexao, role PostgreSQL e versao da migration.

`/healthz` e `/readyz` existem somente nos listeners operacionais: liveness indica processo vivo; readiness valida banco, role e schema a cada chamada e retorna `503` quando algum deles deixa de ser compativel ou acessivel. A superficie publica da API responde `404` para essas rotas. Os defaults operacionais e do Chaos Lab usam loopback; o Compose faz bind interno para containers e publica as portas somente em `127.0.0.1`.

## Comandos

```bash
make help
make fmt
make test
make race
make vet
make staticcheck
make vuln
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
internal/platform/   configuracao, logging, problem details e lifecycle
db/migrations/       migrations versionadas e fail-closed
deployments/         Dockerfiles e bootstrap local do PostgreSQL
api/                 contrato OpenAPI a partir da Sprint 1
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
