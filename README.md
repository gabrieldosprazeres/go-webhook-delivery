# Webhook Delivery Engine

[![CI](https://github.com/gabrieldosprazeres/go-webhook-delivery/actions/workflows/ci.yml/badge.svg)](https://github.com/gabrieldosprazeres/go-webhook-delivery/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539?logo=openapiinitiative&logoColor=white)](api/openapi.yaml)
[![Release](https://img.shields.io/badge/release-v1.2.0-blue)](CHANGELOG.md)

Serviço de entrega confiável de webhooks escrito em Go. O projeto demonstra ingestão
idempotente, entrega `at-least-once`, retries, leases com fencing, isolamento
multi-tenant, assinatura HMAC, defesa SSRF e operação observável.

> Estado atual: release v1.2.0 com console web e observabilidade operacional privada.
> O motor nunca promete `exactly-once`.

**Demonstração:** [vitrine pública](https://webhooks.gabrieldosprazeres.com.br) ·
[Swagger/OpenAPI](https://docs.webhooks.gabrieldosprazeres.com.br) ·
[descoberta da API](https://api.webhooks.gabrieldosprazeres.com.br) ·
[console autenticado](https://console.webhooks.gabrieldosprazeres.com.br).

## Problema e arquitetura

Enviar um `POST` não significa que um webhook foi entregue. O destinatário pode estar
indisponível, responder com rate limit ou processar o evento sem que a resposta chegue
ao remetente. Este engine persiste o trabalho antes de responder `202 Accepted`,
registra cada tentativa e oferece retry, dead-letter e replay com histórico íntegro.

```mermaid
flowchart LR
    Client[Produtor] -->|API key + evento| API[API Go]
    Operator[Usuário] -->|sessão curta| Console[Console Go SSR]
    Console -->|RLS + quotas| PG
    API -->|transação atômica| PG[(PostgreSQL 17)]
    PG -->|claim SKIP LOCKED| Worker[Workers Go]
    Worker -->|HTTPS + HMAC| Consumer[Consumidor]
    Consumer -->|sucesso / falha / 429| Worker
    Worker -->|attempt, retry ou DLQ| PG
    API -. métricas e traces .-> Obs[Collector / Prometheus / Tempo / Grafana]
    Worker -. métricas e traces .-> Obs
    Console -. métricas e traces .-> Obs
```

O PostgreSQL atua como fonte de verdade e fila durável. API e worker são binários
separados e podem escalar independentemente, mas permanecem no mesmo monólito modular
enquanto não existir evidência operacional que justifique novos serviços.

## Stack

- Go 1.27.1, `net/http` e `log/slog`;
- PostgreSQL 17 como fonte de verdade e fila durável;
- Goose para migrations explícitas;
- Prometheus e OpenTelemetry OTLP/HTTP para observabilidade;
- `go test`, race detector, vet, Staticcheck e `govulncheck`;
- Docker Compose para ambiente local e uma topologia de produção no EasyPanel.

As ferramentas Go ficam pinadas no `go.mod` e são executadas com `go tool`.

## Pré-requisitos

- Go 1.27.1;
- Docker 29+ com Docker Compose;
- GNU Make e `rg` (ripgrep).

O smoke opcional de navegador também usa Node.js 26 e Chromium via Playwright; Node
não entra nas imagens nem no runtime do produto.

## Início rápido

A jornada reproduzível completa cria um PostgreSQL efêmero isolado, compila os binários e demonstra bootstrap, HMAC, rotação com assinatura dupla, replay, retry, DLQ e métricas. Em uma máquina com os pré-requisitos, ela termina em menos de 10 minutos e remove credenciais/processos/volume ao sair:

```bash
make quickstart
```

Os artefatos sensíveis ficam em um diretório temporário `0700`; a credencial sintética é criada com modo `0600` e não aparece na linha de comando ou na saída. Portas alternativas podem ser definidas por `WDE_DEMO_{POSTGRES,API,API_OPS,WORKER_OPS,CHAOS}_PORT`; o console mantido pelo Compose usa `WDE_CONSOLE_PORT` e `WDE_CONSOLE_OPS_PORT`.

Para manter o ambiente manual em execução:

```bash
cp .env.example .env
docker compose --profile core up --build
```

O profile `core` inicia PostgreSQL 17, executa a migration em um job separado e somente depois inicia API, worker e console. Os processos de runtime nunca executam migrations.

Em outro terminal:

```bash
curl -i http://127.0.0.1:9090/livez
curl -i http://127.0.0.1:9090/readyz
curl -i http://127.0.0.1:9090/metrics
```

O console local fica em [http://127.0.0.1:8082](http://127.0.0.1:8082). Cole no login
a `api_key` gerada pelo bootstrap. A chave é validada somente nessa requisição e trocada
por uma sessão opaca de 15 minutos de inatividade e 60 minutos absolutos. Dentro do
painel é possível cadastrar endpoints, publicar eventos, acompanhar delivery/tentativas,
pedir replay e ver métricas agregadas das últimas 24 horas do próprio workspace.

Para incluir o Chaos Lab isolado:

```bash
docker compose --profile demo up --build
curl -i http://127.0.0.1:8081/livez
```

Todas as portas publicadas pelo Compose são vinculadas à interface loopback. As
credenciais presentes em `.env.example` são exclusivamente locais e descartáveis. O
Chaos Lab em container serve para inspeção direta; a demo ponta a ponta usa binários
locais porque a política SSRF bloqueia corretamente endereços privados da bridge
Docker.

Os listeners operacionais usam loopback por padrão. Se um orquestrador exigir bind não-loopback, `/livez`, `/readyz` e `/metrics` devem permanecer em rede privada com ACL/NetworkPolicy explícita, sem ingress público; o processo não implementa autenticação nessa superfície.

Antes da primeira chamada, crie o workspace e a API key com o comando one-shot (ele não inicia listener e revela a chave uma única vez):

```bash
WDE_PROFILE=local \
WDE_ADMIN_DATABASE_URL='postgres://wde_admin:admin-local-only@127.0.0.1:5432/wde?sslmode=disable' \
WDE_DATABASE_URL='postgres://wde_api:api-local-only@127.0.0.1:5432/wde?sslmode=disable' \
go run ./cmd/api credentials bootstrap --name 'Demo local' \
  --output-file='./local-credentials.json'
```

O arquivo criado contém `api_key`. O [quickstart detalhado](docs/quickstart.md) traz os exemplos curl para endpoint, evento, consulta, replay, rotação e métricas; o contrato completo está em [`api/openapi.yaml`](api/openapi.yaml). Produção aceita somente destinos HTTPS; HTTP é permitido apenas em loopback nos profiles local/test com a flag explícita.

## Demonstração pública no EasyPanel

O repositório inclui uma topologia produtiva separada em
[`compose.easypanel.yaml`](compose.easypanel.yaml). Ela publica quatro superfícies de produto:

- `showcase:8080`: landing page em pt-BR para recrutadores e apresentação do case;
- `swagger:8080`: contrato OpenAPI navegável;
- `api:8080`: API real, autenticada e isolada das probes operacionais.
- `console:8082`: painel SSR autenticado por sessão derivada da API key.

PostgreSQL, migration, worker, Collector, Prometheus, Tempo e portas operacionais não
recebem domínio ou porta pública. Grafana é uma superfície exclusiva do operador,
autenticada, ligada somente ao loopback do host e acessada por túnel SSH; ela permanece
separada da demonstração para usuários. No host único,
API/worker/console/migrator acessam o PostgreSQL por um volume de
socket Unix protegido; o banco usa SCRAM e não abre listener TCP. Segredos são
montados como arquivos `0400` no UID do processo e nunca entram na imagem ou no Git.

O guia completo, incluindo geração de segredos, configuração de domínios, rollback
e limitações honestas da demo, está em
[Deploy no EasyPanel](docs/easypanel-deployment.md). A topologia exata é exercitada
na CI com `make easypanel-smoke` antes da publicação.

## Execução sem containers

```bash
export WDE_PROFILE=local
export WDE_DATABASE_URL='postgres://wde_api:api-local-only@127.0.0.1:5432/wde?sslmode=disable'
go run ./cmd/api
```

O worker usa a credencial `wde_worker`. O Chaos Lab não recebe DSN e falha no startup se for iniciado com profile `production`.
Por padrão, o Chaos Lab escuta somente em `127.0.0.1:8081`; no container local o Compose faz override do listener e publica o port exclusivamente em loopback.

## Configuração

Variáveis não secretas podem ser substituídas por flags homônimas, como `--profile`, `--http-addr`, `--log-level` e `--shutdown-timeout`. Segredos e DSNs não possuem flag para evitar exposição no histórico/process list.

Principais variáveis:

| Variável | Uso |
|---|---|
| `WDE_PROFILE` | `local`, `test` ou `production` |
| `WDE_DATABASE_URL` | DSN da role específica do processo |
| `WDE_DATABASE_LOCAL_SOCKET` | opt-in fail-closed para o socket `/var/run/postgresql`; default `false` |
| `WDE_ADMIN_DATABASE_URL` | DSN usada exclusivamente pelos comandos one-shot de credenciais |
| `WDE_DATABASE_TIMEOUT` | prazo para startup/readiness do banco; default `5s` |
| `WDE_OTEL_EXPORTER_OTLP_ENDPOINT` | endpoint OTLP/HTTP opcional; vazio mantém traces no-op |
| `WDE_OTEL_EXPORT_TIMEOUT` | prazo bounded de export e flush; default `5s`, máximo `10s` |
| `WDE_OTEL_TRACE_SAMPLE_RATIO` | amostragem entre `0` e `1`; default `0.1` |
| `WDE_API_HTTP_ADDR` | listener da API; default `:8080` |
| `WDE_API_OPERATIONAL_ADDR` | probes da API; default `127.0.0.1:9090` |
| `WDE_WORKER_OPERATIONAL_ADDR` | probes do worker; default `127.0.0.1:9091` |
| `WDE_CONSOLE_HTTP_ADDR` | listener do console; default `:8082` |
| `WDE_CONSOLE_OPERATIONAL_ADDR` | probes privadas do console; default `127.0.0.1:9092` |
| `WDE_CONSOLE_ORIGIN` | origin exata usada na defesa CSRF; HTTPS obrigatório em produção |
| `WDE_OTEL_ALLOW_PRIVATE_HTTP` | opt-in produtivo somente para `http://otel-collector:4318` na rede privada |
| `WDE_CHAOSLAB_HTTP_ADDR` | listener local; default `127.0.0.1:8081` |
| `WDE_ALLOW_HTTP_DESTINATIONS` | habilita explicitamente destinos HTTP loopback fora de produção |
| `WDE_OTEL_EXPORT_TIMEOUT` | orçamento do export/flush, obrigatório entre `100ms` e `10s`; default `5s` |
| `WDE_OTEL_TRACE_SAMPLE_RATIO` | razão local finita entre `0` e `1`; default `0.1`, nunca herdada do bit sampled remoto |
| `WDE_EDGE_{MAX_IN_FLIGHT,GLOBAL,ORIGIN,PREFIX,WINDOW,MAX_BUCKETS}` | semáforo e limiter local bounded antes do lookup de credencial |
| `WDE_QUOTA_<OPERACAO>_{GLOBAL,WORKSPACE,API_KEY}` | limites persistentes por janela para ingestão, escrita de endpoint, consulta e replay |
| `WDE_QUOTA_REPLAY_RESOURCE` | limite de replay por delivery na janela; default `2/min` |
| `WDE_QUOTA_<OPERACAO>_WINDOW` | janela fixa PostgreSQL da quota (`1s` para ingestão; `1m` para as demais) |
| `WDE_WORKER_CONCURRENCY` | jobs HTTP simultâneos; default `8` |
| `WDE_WORKER_CLAIM_BATCH_SIZE` | claims por ciclo, sempre menor ou igual à concorrência; default `8` |
| `WDE_WORKER_WORKSPACE_BATCH_LIMIT` | teto de claims por workspace em um batch; default `2` |
| `WDE_WORKER_ENDPOINT_BATCH_LIMIT` | teto de claims por endpoint em um batch; default `2` |
| `WDE_WORKER_POLL_INTERVAL` | base do backoff com jitter quando a fila está vazia; default `250ms` |
| `WDE_WORKER_CLAIM_TIMEOUT` | deadline de cada acesso de claim ao banco; default `200ms`, sempre menor ou igual ao poll e ao lease |
| `WDE_WORKER_REQUEST_TIMEOUT` | timeout HTTP total; default `10s`, máximo `20s` |
| `WDE_WORKER_LEASE_TTL` | validade do lease; default `30s` e margem mínima de `10s` sobre o timeout |
| `WDE_WORKER_RETRY_BASE` / `WDE_WORKER_RETRY_CAP` | janela exponencial com full jitter; defaults `1s` / `15m` |
| `WDE_SHUTDOWN_TIMEOUT` | prazo de shutdown; default `30s` |
| `WDE_LOG_LEVEL` | `debug`, `info`, `warn` ou `error` |
| `WDE_INGRESS_TLS_TERMINATED` | declaração obrigatória para API em produção |
| `WDE_*_FILE` | paths de peppers e keyrings montados em produção; rate limit e cursor usam peppers independentes |

No profile `production`, conexões TCP ao PostgreSQL exigem `sslmode=verify-full`. A
alternativa local exige `WDE_DATABASE_LOCAL_SOCKET=true`, host vazio, exatamente o
socket `/var/run/postgresql` e `sslmode=disable`; combinações ambíguas falham
fechado. O socket exige `passfile` regular, pertencente ao processo e com modo máximo
`0600`; DSNs produtivos não podem conter senha embutida. O startup também falha se TLS de entrada não estiver declarado,
profiling/HTTP externo estiver habilitado ou os secret files estiverem ausentes,
forem symlinks, tiverem permissões acima de `0600` ou pertencerem a outro UID.

O scheduler nunca reserva mais que os slots livres. Uma sequence monotônica, sem row lock global, alimenta cursores persistidos por workspace e endpoint inclusive entre batches unitários e workers concorrentes. Workspace, endpoint runtime e delivery usam locks locais com `SKIP LOCKED`, portanto um tenant travado não impede progresso independente. Cada claim possui deadline de banco explícito. Polling vazio usa backoff exponencial com jitter para evitar sincronização entre instâncias. Um lease expirado abandona a tentativa antiga e incrementa o fencing token antes de novo envio. Resultados com owner/token vencidos afetam zero linhas.

Antes do lookup de credencial, cada instância aplica semáforo global e limiter fixed-window por volume global, origem observada no socket e prefixo apenas sintático. A memória usa LRU com cardinalidade máxima, identificadores HMAC e ignora headers de origem enviados pelo cliente. Depois da autenticação, a quota persistente roda antes da autorização por escopo.

Quotas autenticadas usam buckets PostgreSQL em uma única transação para dimensões
global, workspace, API key e, no replay, delivery. O relógio da janela é
`transaction_timestamp()`, portanto todas as dimensões da mesma requisição compartilham
o mesmo boundary. Reiniciar ou multiplicar instâncias não zera contadores; buckets
expirados são removidos em lotes limitados. Identificadores de dimensão incluem versão,
operação, janela e tenant/recurso no HMAC com pepper exclusivo e nunca viram labels de
métrica.

`POST /v1/deliveries/{id}/replays` exige `deliveries:retry`, motivo, `Idempotency-Key` e quota. Um replay válido cria novo `run_number`, zera somente o contador do run e preserva `attempt_sequence`, fencing e histórico. Comandos concorrentes idênticos retornam o mesmo comando; conteúdo divergente gera `409`; payload expurgado falha fechado. Comando, transição e auditoria confirmam ou revertem juntos.

`POST /v1/endpoints/{id}/secret-rotations` exige `endpoints:write` e `Idempotency-Key`. A nova chave fica `active` e a anterior `retiring`; durante a janela de 1 hora a 7 dias cada tentativa carrega as duas assinaturas no header `WDE-Signature`. Uma nova rotação é recusada enquanto houver overlap ativo. Depois da janela, o worker purga somente o envelope antigo; hash e fingerprint do comando sobrevivem por `metadata_retention_days`. Um retry idêntico cujo segredo já foi purgado retorna `409 rotation_result_expired`, e conteúdo divergente continua retornando conflito sem nova rotação.

O cliente de saída canonicaliza IDNs, rejeita credenciais/query/fragmento e formatos ambíguos, resolve todos os A/AAAA a cada nova conexão e falha se qualquer endereço não for publicamente alcançável segundo o registro especial da IANA. IPv6 é aceito somente no espaço global atualmente alocado, com exceções IANA explícitas; NAT64, 6to4, SRv6 local, documentação, benchmark, metadata e mapped IPv6 permanecem bloqueados. O IP validado é fixado no `DialContext`; hostname/SNI e validação TLS permanecem intactos. Proxy ambiental e redirects são ignorados/bloqueados.

Payloads e segredos usam AES-256-GCM com keyrings separados fora do banco. O envelope v2 vincula por AAD versão, workspace, tipo, recurso e metadados imutáveis. Nonce é aleatório por escrita; versões desconhecidas, transplante e adulteração falham fechado. A leitura do formato v1 permanece somente para upgrade seguro.

O worker executa purge horário e drena batches dentro de um budget de 15 minutos. Payload expirado invalida fencing, abandona attempts em andamento, terminaliza a delivery com `payload_expired` e só então zera o envelope; o replay posterior é recusado. Attempts, comandos, deliveries, events, auditoria e buckets vencidos também são removidos automaticamente. O loop usa progresso por sweep e consulta o backlog exato somente ao estabilizar, evitando scans quadráticos. O estado agregado publica em log estruturado contagens, backlog acionável, duração e idade mais antiga atual sem identificador de tenant. Exclusão de workspace revoga/bloqueia primeiro, serializa com rotação de segredo e avança por checkpoints pequenos com lease/fencing e rewind defensivo; tombstone e auditoria só registram conclusão após a última etapa.

Após restaurar um backup, mantenha API/worker fora do tráfego e execute a quarentena antes de qualquer readiness:

```bash
go run ./cmd/api restore quarantine --generation 1
# valide as revogações e reconcilie o snapshot
go run ./cmd/api restore reconcile --generation 1
```

A quarentena revoga todas as API keys e HMAC secrets restauradas, suspende workspaces/endpoints e invalida leases. `restore reconcile` só libera readiness se nenhuma credencial, workspace, endpoint ou delivery de egress estiver ativa. Para exclusão administrativa: `go run ./cmd/api workspace purge --id <workspace-uuid>`.

Cursores de listagem são binários, versionados, vinculados ao workspace e autenticados com HMAC. Alteração de qualquer byte ou uso em outro tenant retorna `400 invalid_page` sem revelar dados.

Timeouts, erros de rede, `408`, `425`, `429` e `5xx` recebem retry. Redirects, os
demais `4xx` e status fora de `100..599` são falhas permanentes. O atraso usa full
jitter exponencial; `Retry-After` só é aceito quando válido e dentro do teto, caso
contrário a política padrão prevalece. A última falha transitória termina em
`dead_letter`, preservando todas as tentativas. Em `SIGINT`/`SIGTERM`, o worker para
novos claims, drena jobs, cancela os restantes antes do fim e retorna no deadline
absoluto de no máximo 30 segundos mesmo diante de dependência não cooperativa.

Peppers produtivos usam uma linha `v1:<base64url-sem-padding>` com exatamente 32 bytes
aleatórios. Keyrings usam JSON versionado, também com chaves de exatamente 32 bytes:

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

`/livez`, `/readyz` e `/metrics` existem somente nos listeners operacionais. Liveness indica apenas processo vivo. Readiness valida role/schema lógico v6, quarentena de restore e keyrings; no worker também exige scheduler ativo e retenção saudável. As superfícies públicas da API e do console não expõem métricas operacionais. Não há `pprof` registrado em produção.

Métricas usam somente dimensões bounded (`route`, método, classe HTTP, outcome e categoria). Nunca há workspace, endpoint, evento, delivery, IP ou URL em labels. Traces correlacionam request, evento, delivery e tentativa por IDs opacos; não capturam payload, API key, HMAC, URL, query, headers, ciphertext ou resposta externa. O exportador OTLP é opcional e sua indisponibilidade não interrompe ingestão/entrega. Em produção ele usa HTTPS, exceto pelo opt-in estrito do Collector no hostname fixo da rede Docker interna. O usuário vê métricas tenant-scoped calculadas no PostgreSQL; Prometheus/Grafana/Tempo são ferramentas do operador. O Tempo usa armazenamento efêmero com teto de 256 MiB, limites de ingestão e alerta de descarte, evitando crescimento irrestrito na VPS.

O Chaos Lab local oferece `POST /success`, `/fail-n`, `/timeout`, `/rate-limit`, `/permanent-failure` e `/verify-signature`, além de `/configure`, `/reset` e o estado agregado seguro em `GET /state`. Parâmetros opcionais de teste têm limites estritos; os defaults funcionam em URLs de webhook sem query. Ele nunca ecoa corpo ou segredo.

Um consumidor mínimo que valida o corpo bruto em tempo constante e rejeita timestamps fora da janela de cinco minutos está em [`examples/hmac-consumer`](examples/hmac-consumer). Durante rotação, consumidores devem aceitar as duas entradas separadas por `;` no header `WDE-Signature` até o fim do overlap.

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
make quickstart
make adversarial
make benchmark
make soak
make container-smoke
make easypanel-smoke
make browser-smoke
WDE_SUPPLY_CHAIN_OUTPUT="$(mktemp -d)" make supply-chain
make secret-scan
make check
```

O benchmark local de referência, após warmup excluído e três rodadas independentes de
1.000 eventos, superou o alvo de ingestão, mas não o de delivery: medianas de 440,00
ingestões/s, p95 de ingestão 15,08 ms e 32,77 deliveries/s. O soak concluiu
3.000/3.000 IDs distintos, sem duplicata, missing, falha ou backlog. Ambiente,
dispersão, séries de recursos e a divergência estão publicados em
[Benchmark e soak](docs/benchmark-results.md); esses números não são promessa de SLO.

Para aplicar migrations fora do Compose:

```bash
export WDE_MIGRATOR_DATABASE_URL='postgres://wde_migrator:migrator-local-only@127.0.0.1:5432/wde?sslmode=disable'
make migrate-up
```

## Estrutura

```text
cmd/                 composition roots de api, worker, console, chaoslab, showcase e docs
internal/auth/       API keys, principal e bootstrap/revogacao
internal/endpoint/   destinos, subscriptions e segredo de assinatura
internal/event/      ingresso idempotente e fan-out transacional
internal/delivery/   claim, envio HTTP e timeline
internal/console/    painel SSR/BFF, CSP, CSRF e jornadas humanas
internal/consolesession/ sessões opacas, expiração, revogação e auditoria
internal/insights/   métricas tenant-scoped das últimas 24 horas
internal/outboundhttp/ parser, resolução e dialer anti-SSRF
internal/retention/  purge, exclusão e restore quarantine
internal/signing/    protocolo HMAC v1
internal/platform/   configuracao, criptografia, logging, telemetria e lifecycle
examples/            consumidor HMAC verificável
scripts/             demonstração local efêmera
db/migrations/       migrations versionadas e fail-closed
deployments/         Dockerfiles e bootstrap local/produtivo do PostgreSQL
api/                 contrato OpenAPI
test/                suites de integração e adversariais
docs/                PRD, arquitetura, ADRs, segurança, backlog e status
```

## Decisões e segurança

- [Changelog](CHANGELOG.md)
- [Arquitetura](docs/webhook-delivery-engine-architecture.md)
- [Arquitetura de dados](docs/webhook-delivery-engine-data-architecture.md)
- [Backlog do MVP](docs/webhook-delivery-engine-backlog.md)
- [ADRs](docs/adr/)
- [Status](docs/webhook-delivery-engine-status.md)
- [Política de segurança](SECURITY.md)
- [Auditoria de segurança final](docs/webhook-delivery-engine-security-audit.md)
- [Auditoria de segurança do deploy EasyPanel](docs/webhook-delivery-engine-security-audit-easypanel.md)
- [Auditoria de segurança do console e observabilidade](docs/webhook-delivery-engine-security-audit-console-observability.md)
- [QA final da Sprint 6](docs/webhook-delivery-engine-qa-sprint-6.md)
- [Runbook operacional](docs/operations-runbook.md)
- [Demonstração de portfólio](docs/demo-runbook.md)
- [Deploy no EasyPanel](docs/easypanel-deployment.md)
- [Supply chain e imagens](docs/supply-chain.md)
- [Checklist de release](docs/release-checklist.md)

Payloads, API keys, HMAC secrets, assinaturas, headers, URLs completas, DSNs e corpos externos nunca devem entrar em logs, traces, métricas ou erros. O logger da fundação aplica uma allowlist de atributos e possui teste canário contra vazamento.
