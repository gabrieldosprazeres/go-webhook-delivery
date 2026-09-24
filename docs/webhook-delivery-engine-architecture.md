# Arquitetura: Webhook Delivery Engine

**Status:** Proposta para Security Review da arquitetura  
**Versão:** 1.0  
**Data:** 2026-09-23  
**Escopo:** MVP API-first, sem frontend  
**Decisões detalhadas:** `docs/adr/`

## 1. Visão executiva

O Webhook Delivery Engine será um **monólito modular em Go 1.27**, distribuído como três binários independentes no mesmo módulo e repositório:

- `api`: autentica, autoriza e persiste configurações, eventos e comandos operacionais;
- `worker`: reserva entregas, realiza HTTP de saída e finaliza tentativas;
- `chaoslab`: receptor exclusivamente local para cenários de demonstração e testes.

API e worker compartilham os mesmos módulos de negócio e o mesmo PostgreSQL, mas têm composição, credenciais de banco e permissões diferentes. PostgreSQL é simultaneamente fonte de verdade e fila durável. Não há Redis, Kafka, Kubernetes, ORM, framework HTTP ou contêiner de injeção de dependência no MVP.

A arquitetura privilegia invariantes demonstráveis:

1. `202 Accepted` existe somente depois do commit atômico de evento e entregas;
2. o tenant vem exclusivamente da API key autenticada;
3. entrega é `at-least-once`, nunca `exactly-once`;
4. nenhuma chamada externa ocorre dentro de transação;
5. lease expirado não autoriza finalização: fencing é obrigatório;
6. o transporte de saída conecta ao IP que foi resolvido e validado;
7. payloads e segredos recuperáveis ficam cifrados no nível da aplicação;
8. logs, traces, métricas e auditoria não recebem payloads nem credenciais.

Esta arquitetura absorve `SEC-001` a `SEC-025` do Security Review do PRD. O projeto permanece classificado como laboratório com dados sintéticos até que a revisão de segurança da arquitetura e do schema seja aprovada e o runbook de incidentes exista.

## 2. Direcionadores e restrições

### 2.1 Direcionadores

- Confiabilidade sob crash, timeout, repetição e concorrência.
- Segurança diante de clientes, payloads, DNS e destinos hostis.
- Operação local reproduzível em até dez minutos.
- Código Go idiomático, explícito e pequeno o suficiente para ser compreendido.
- Evidência por testes, race detector, falhas simuladas e benchmarks documentados.

### 2.2 Restrições do MVP

- Go 1.27.x e PostgreSQL 17.x.
- Sem frontend, login humano, broker externo ou serviço comercial obrigatório.
- HTTPS em destinos; HTTP somente no perfil `local`, impossível de habilitar no perfil `production`.
- Um único módulo Go e um único schema lógico, com papéis de banco separados por processo.
- Escala-alvo de laboratório: pelo menos 100 eventos/s e 100 entregas/s no cenário de referência.

### 2.3 Princípios

- Monólito primeiro; extração de serviço exige métrica e ADR.
- Pacotes por capacidade, não por camada genérica.
- Dependências apontam para o negócio; adaptadores dependem de interfaces pequenas definidas pelo consumidor.
- Tipos e transições inválidas são recusados na borda e no banco.
- Defaults produtivos são seguros; configuração inválida impede startup.
- Não criar abstração sem dois usos reais ou uma fronteira externa testável.

## 3. Contexto C4 textual

### 3.1 Nível 1 — Sistema

```text
[Aplicação produtora]
      | HTTPS + API key + Idempotency-Key
      v
[Webhook Delivery Engine] ---- OTLP/Prometheus ----> [Stack de observabilidade]
      |
      | HTTPS + HMAC versionado
      v
[Endpoint consumidor não confiável]

[Operador técnico] -- API autenticada --> [Webhook Delivery Engine]
[Avaliador local] -- cenários sintéticos --> [Chaos Lab]
```

O produtor publica eventos. O Engine persiste, agenda e entrega. O consumidor é externo e hostil por padrão. O operador consulta a timeline e solicita replay. O Chaos Lab não é parte da superfície produtiva.

### 3.2 Nível 2 — Contêineres

```text
                    rede pública/API
[Produtor/Operador] ------------------> [api :8080]
                                             |
                                             | SQL, role wde_api
                                             v
                                      [PostgreSQL 17]
                                             ^
                                             | SQL, role wde_worker
                                             |
                                      [worker, sem porta pública]
                                             |
                                             | HTTPS fixado ao IP validado
                                             v
                                      [Endpoints externos]

[api :9090 interno] ------ métricas/probes ------> [coletor]
[worker :9090 interno] --- métricas/probes ------> [coletor]
[chaoslab local :8081] <-- somente profile local -- [worker local]
```

- `api` é stateless fora do PostgreSQL e da configuração em memória.
- `worker` mantém somente capacidade e tarefas em andamento; todo trabalho recuperável está no banco.
- cada processo expõe um listener operacional separado, ligado a loopback por padrão;
- `chaoslab` usa imagem/perfil Compose separado e nunca integra a imagem ou configuração de produção.

### 3.3 Nível 3 — Componentes internos

```text
apihttp -> auth -> endpoint/event/delivery/audit -> portas consumidoras
                                                     |
                                                     v
                                             postgres adapters

worker runtime -> delivery scheduler -> outboundhttp/ssrf
                       |                    |
                       v                    v
                  signing/cryptox       destino HTTPS
                       |
                       v
                  postgres adapters

todos -> observability (slog, métricas, tracing) sem conteúdo sensível
```

O handler traduz HTTP para comandos e consultas. Serviços de capacidade mantêm regras e coordenam transações através de portas estreitas. Adaptadores PostgreSQL e HTTP implementam essas portas. Não há acesso direto ao SQL nos handlers.

## 4. Stack e dependências

| Área | Escolha | Motivo |
|---|---|---|
| Linguagem | Go 1.27.x, patch pinado em CI e imagem | Concorrência, toolchain, binários simples e perfil do projeto |
| HTTP de entrada | `net/http`, `http.ServeMux`, `encoding/json` | O roteador padrão atende o contrato sem framework |
| Logs | `log/slog` com handler JSON | Estruturado, padrão e compatível com redação por allowlist |
| PostgreSQL | `github.com/jackc/pgx/v5` e `pgxpool` | Driver idiomático, controle explícito de transações e tipos |
| SQL | SQL escrito à mão e `sqlc` para geração tipada | SQL revisável sem ORM/reflection |
| Migrações | Goose pinado como tool dependency | Migrações versionadas, `up/down` quando reversão for segura |
| Concorrência | goroutines, channels limitados, `context`, `errgroup` | Primitivas idiomáticas e cancelamento estruturado |
| Telemetria | OpenTelemetry Go + exporter Prometheus/OTLP | Instrumentação vendor-neutral |
| Teste de integração | `testing`, `httptest`, `testing/synctest` e Testcontainers | Banco real, HTTP real e tempo determinístico onde aplicável |
| Segurança de código | `go vet`, Staticcheck, `govulncheck`, scanner de segredo/imagem | Gates reproduzíveis |

Dependências de ferramentas serão pinadas no `go.mod` com `go get -tool` e executadas com `go tool`. Nenhuma biblioteca entra sem necessidade, licença compatível, avaliação de manutenção e cobertura no scanner.

## 5. Estrutura do repositório

```text
.
├── AGENTS.md
├── api/
│   └── openapi.yaml
├── cmd/
│   ├── api/main.go                 # composition root da API
│   ├── worker/main.go              # composition root do worker
│   └── chaoslab/main.go            # receptor local isolado
├── db/
│   ├── migrations/                 # schema versionado
│   ├── queries/                    # SQL consumido pelo sqlc
│   └── sqlc.yaml
├── deployments/
│   ├── compose.yaml                # ambiente local
│   └── docker/                     # Dockerfiles multi-stage
├── docs/
│   ├── adr/
│   ├── threat-model.md
│   ├── runbook.md
│   └── webhook-delivery-engine-*.md
├── internal/
│   ├── apihttp/                    # rotas, middleware, problem+json
│   ├── auth/                       # API keys, escopos e tenant context
│   ├── endpoint/                   # endpoints, filtros e rotação
│   ├── event/                      # ingestão e idempotência
│   ├── delivery/                   # estados, claims, retry, DLQ e replay
│   ├── audit/                      # eventos de auditoria append-only
│   ├── retention/                  # purge e exclusão de workspace
│   ├── ratelimit/                  # quotas persistentes e prefilters locais
│   ├── signing/                    # protocolo HMAC versionado
│   ├── outboundhttp/               # transporte seguro e classificação HTTP
│   ├── security/
│   │   ├── cryptox/                # AEAD e keyrings versionados
│   │   └── ssrf/                   # URL, DNS e IP policy
│   ├── observability/              # slog, métricas e OTel
│   ├── config/                     # configuração tipada e validação
│   ├── postgres/
│   │   ├── sqlc/                   # gerado; não editar
│   │   └── *repo.go                # adaptadores por capacidade
│   └── runtime/
│       ├── apiapp/                 # ciclo de vida do binário API
│       ├── workerapp/              # scheduler, pool e shutdown
│       └── chaosapp/               # cenários locais
├── test/
│   ├── integration/                # PostgreSQL e HTTP reais
│   ├── adversarial/                # SSRF, tenancy, leaks, limites
│   └── fixtures/                   # dados sintéticos, nunca segredos reais
└── Makefile                         # comandos finos; lógica permanece em Go/scripts revisáveis
```

Não haverá `pkg/` até existir API Go pública real. Não haverá pacotes `utils`, `helpers`, `common`, `models` ou `services` genéricos. Tipos pertencem à capacidade que lhes dá significado.

## 6. Limites de módulos e regras de dependência

### 6.1 Capacidades

- `auth`: autentica e produz `Principal{KeyID, WorkspaceID, Scopes}`. Nenhum handler aceita `workspace_id` do cliente como autoridade.
- `endpoint`: valida configuração do destino, subscriptions e ciclo de segredos; não realiza entregas.
- `event`: valida/canonicaliza a publicação e coordena o commit de evento + fan-out.
- `delivery`: é dona da máquina de estados e das políticas de claim, retry, DLQ e replay.
- `signing`: recebe bytes imutáveis e segredos temporários; não busca banco nem faz HTTP.
- `outboundhttp`: resolve, valida, conecta e classifica o resultado; não decide transição de negócio.
- `audit`: única porta de inserção para ações sensíveis; não expõe update/delete.
- `retention`: executa purge idempotente e revoga antes de remover.
- `postgres`: implementa armazenamento; não contém decisões de política fora das garantias transacionais.

### 6.2 Regras obrigatórias

1. `cmd/*` e `runtime/*` podem importar todos os módulos necessários para compor o processo.
2. `apihttp` depende dos casos de uso, nunca de `postgres/sqlc`.
3. capacidades não importam `apihttp`, `runtime` nem tipos gerados pelo `sqlc`.
4. interfaces de persistência e relógio ficam no pacote consumidor e contêm somente operações usadas.
5. adaptadores `postgres/*repo.go` dependem da capacidade e mapeiam tipos gerados para tipos de negócio.
6. structs de API/OpenAPI não atravessam a fronteira do handler.
7. `context.Context` é o primeiro parâmetro de toda operação com I/O ou cancelamento; não é armazenado em struct.
8. não há mutable globals, goroutines iniciadas em `init`, service locator nem singleton implícito.
9. toda goroutine tem owner, caminho de cancelamento e espera (`errgroup`/`WaitGroup`).
10. dependências externas são encapsuladas apenas na fronteira em que isso habilita teste ou protege política.

## 7. Composição e injeção de dependência

Cada `main` é um composition root explícito:

1. carrega e valida configuração;
2. configura logger seguro;
3. abre pool PostgreSQL com role do processo;
4. cria keyrings e primitives criptográficas;
5. instancia adaptadores;
6. instancia serviços com construtores;
7. inicia listeners/schedulers em `errgroup`;
8. propaga cancelamento e fecha recursos na ordem inversa.

Construtores retornam erro quando uma invariável não pode ser atendida. Interfaces são pequenas e definidas no consumidor; concrete types são preferidos dentro do módulo. Não haverá framework de DI, reflection ou registry global.

## 8. Superfície HTTP

O contrato definitivo estará em `api/openapi.yaml`. Rotas planejadas:

| Método e rota | Escopo | Comportamento principal |
|---|---|---|
| `POST /v1/endpoints` | `endpoints:write` | cria endpoint e revela segredo uma vez |
| `GET /v1/endpoints/{id}` | `deliveries:read` | lê metadados no tenant |
| `PATCH /v1/endpoints/{id}` | `endpoints:write` | altera estado/filtros com auditoria |
| `POST /v1/endpoints/{id}/secret-rotations` | `endpoints:write` | cria versão e janela de overlap |
| `POST /v1/events` | `events:write` | ingresso idempotente e `202` após commit |
| `GET /v1/events/{id}` | `deliveries:read` | metadados, sem payload |
| `GET /v1/deliveries` | `deliveries:read` | cursor opaco e página limitada |
| `GET /v1/deliveries/{id}` | `deliveries:read` | estado e timeline, sem corpos |
| `POST /v1/deliveries/{id}/replays` | `deliveries:retry` | comando idempotente e auditado |

Todos os erros usam `application/problem+json`, `code` estável e `request_id`. Recursos ausentes e de outro workspace retornam a mesma resposta `404`. CORS é desabilitado. Métodos e content types são allowlisted. A API rejeita bodies além do limite antes de desserializar, com `http.MaxBytesReader`.

Emissão e revogação de credenciais não possuem rota pública no MVP. O próprio binário `api` oferece comandos one-shot `credentials bootstrap`, `credentials issue` e `credentials revoke`, que não iniciam listener, exigem uma role administrativa separada e registram auditoria. Em produção, material recém-emitido é escrito somente em arquivo novo com modo `0600`; stdout é permitido apenas no profile local e quando conectado a TTY. `bootstrap` falha se o banco já possuir workspace, salvo modo adicional explícito e auditado. A role administrativa nunca é usada pelo processo `serve`.

### 8.1 Pipeline de middleware

```text
request ID -> recovery sanitizado -> limites globais -> TLS/profile guard
-> autenticação -> rate limit da credencial/workspace -> autorização por escopo
-> handler -> serialização problem+json -> access log por allowlist
```

Recovery não intercepta `http.ErrServerClosed` nem impede shutdown. Headers, query, body, API key e respostas externas não entram automaticamente em log ou trace.

## 9. Autenticação, tenancy e autorização

### 9.1 API key

- formato: `wde_live_<prefix>_<secret>`; em ambiente local, `wde_test_...`;
- `secret`: 32 bytes de CSPRNG codificados em base64url sem padding (256 bits);
- `prefix`: identificador aleatório não secreto usado somente para lookup e apresentação;
- persistência: prefixo e `HMAC-SHA-256(auth_pepper, token_completo)`; o pepper vem de secret file fora do banco;
- comparação: `subtle.ConstantTimeCompare`, incluindo caminho dummy para prefixo inexistente;
- transporte: somente `Authorization: Bearer`; token em query/cookie/body nunca autentica;
- sem cache de decisões no MVP, para revogação e expiração valerem na próxima requisição em todas as réplicas;
- máximo padrão: 10 chaves ativas por workspace; expiração opcional, revogação imediata e auditoria obrigatória.

Falhas por origem e prefixo passam por limiter local limitado em memória; chaves reconhecidas e workspaces passam também por quota persistente no PostgreSQL. A resposta de autenticação é genérica. IP/prefixo nunca são labels de métrica.

### 9.2 Tenant context

`Principal.WorkspaceID` é criado pelo autenticador e propagado em parâmetro tipado. Toda query de recurso recebe simultaneamente `resource_id` e `workspace_id`; constraints compostas preservam essa relação. A API não aceita header de workspace.

Como defesa adicional, API e worker usam roles distintas, queries geradas não possuem variantes sem tenant para caminhos públicos e testes de arquitetura procuram SQL público sem filtro. RLS poderá ser adicionada pelo Data Architect como segunda barreira se não conflitar com o worker, mas não substitui a filtragem explícita.

## 10. Fluxos críticos

### 10.1 Ingestão idempotente

```text
HTTP -> autenticar/autorizar -> limitar body -> validar JSON/event type/idempotency
     -> canonicalizar conteúdo lógico e calcular fingerprint
     -> checar quotas e fan-out na mesma visão transacional
     -> BEGIN
          inserir evento ou encontrar (workspace_id, idempotency_key)
          se existente: comparar fingerprint; igual retorna ID, diferente conflito
          se novo: selecionar endpoints elegíveis no workspace
                   validar fan-out <= 100
                   cifrar payload e inserir evento
                   inserir todas as deliveries
                   inserir auditoria mínima quando aplicável
        COMMIT
     -> 202 Accepted
```

O fingerprint persistido é HMAC-SHA-256, com pepper próprio fora do banco, sobre a versão do esquema de idempotência, tipo, payload JSON canonicalizado conforme RFC 8785 e seletor explícito de destinos. O hash da `Idempotency-Key` usa outro pepper, separado também do verificador de API keys. Isso reduz oráculos offline sobre payloads de baixa entropia em um dump. Os bytes originais validados são cifrados e preservados para entrega; canonicalização serve somente à comparação. `READ COMMITTED` com constraint única `(workspace_id, idempotency_key_hash)` é suficiente; conflitos são relidos dentro da unidade de trabalho. Nenhuma resposta `202` ocorre antes do commit.

### 10.2 Claim

```text
capacidade livre N -> BEGIN curto
  selecionar candidatas por cursor persistido de workspace/endpoint e next_attempt_at/id
  FOR UPDATE OF workspace/endpoint_runtime/delivery SKIP LOCKED
  obter número monotônico com sequence PostgreSQL, sem row lock global
  no máximo N e limites por workspace/endpoint no batch
  serializar claims de um mesmo endpoint por lock de sua linha de capacidade
  conferir concorrência ativa por endpoint
  status=processing, lease_owner, lease_expires_at
  fencing_token = fencing_token + 1
  criar attempt append-only em estado started
COMMIT -> executar HTTP fora da transação
```

O relógio autoritativo é sempre o PostgreSQL em UTC: `statement_timestamp()` fixa o instante de elegibilidade e permite planos indexados; `clock_timestamp()` registra transições, leases e auditoria. O batch nunca excede slots livres. Cada acesso de claim ao banco recebe contexto com deadline default de `200ms`, limitado a `2s` e nunca maior que poll ou lease. O scheduler usa polling com jitter e backoff vazio; `LISTEN/NOTIFY` fica adiado porque não é necessário para a meta inicial.

### 10.3 Entrega

```text
carregar snapshot da tentativa -> decifrar payload e keyring pelo menor escopo
-> resolver DNS com timeout -> validar TODOS A/AAAA -> escolher IP permitido
-> construir headers allowlisted e assinatura(s) -> HTTP com deadline
-> limitar e sanitizar resposta -> apagar referências ao plaintext
-> BEGIN curto
     inserir/finalizar attempt append-only
     UPDATE delivery ...
       WHERE id/workspace/status/lease_owner/fencing_token conferem
         AND lease_expires_at > clock_timestamp()
     liberar capacidade do endpoint
   COMMIT
```

Se o `UPDATE` afetar zero linhas, o resultado é obsoleto e não muda a entrega. Ele ainda produz métrica técnica sem payload. O timeout HTTP total padrão é 10 s e o lease padrão é 30 s; a validação de configuração exige `lease_ttl >= request_timeout + 10 s`. Extensão de lease será adicionada somente se surgirem timeouts legítimos maiores.

### 10.4 Classificação, retry e DLQ

- `2xx`: `succeeded`;
- timeout, erro de rede, DNS transitório, `408`, `425`, `429`, `5xx`: retry;
- `3xx`: falha permanente no MVP, pois redirects são desabilitados;
- outros `4xx`: `failed_permanent`;
- violação de política SSRF/TLS/configuração: falha permanente e auditável, nunca fallback inseguro.

Retry usa exponential backoff com full jitter: valor aleatório uniforme entre zero e `min(base*2^attempt, cap)`. Defaults: base 1 s, cap 15 min, máximo 10 tentativas. `Retry-After` válido e dentro do teto pode elevar a espera; valor inválido ou acima do teto é ignorado e cai na política full-jitter padrão. Ao esgotar a política, estado `dead_letter`.

### 10.5 Replay

Replay exige escopo, motivo entre 1 e 500 caracteres, `Idempotency-Key` de até 128 bytes e quota persistente. A transação bloqueia a delivery, valida tenant/estado/payload presente, insere comando único por `(workspace_id, idempotency_key)`, registra auditoria e cria nova elegibilidade sem alterar attempts anteriores. Delivery `succeeded`, payload expurgado ou comando concorrente inválido é recusado.

### 10.6 Shutdown e crash

API, ao receber `SIGTERM`/`SIGINT`, marca readiness negativa, para aceite, executa `http.Server.Shutdown` com prazo e fecha pool/telemetria.

Worker marca readiness negativa, cancela o loop de claims e aguarda tarefas em andamento. Antes do prazo cancela seus contexts e, no deadline absoluto de no máximo 30 s, o runner retorna mesmo se transporte ou store ignorar cancelamento; o processo então encerra. Pânicos de claim/job são capturados como erro. Não força finalização de resultado sem fencing válido. Jobs não finalizados retornam à elegibilidade pela expiração do lease até `TTL + 5 s`.

Crash abrupto pode deixar `attempt` iniciado; o próximo claim preserva esse registro como abandonado e cria novo attempt. O histórico nunca é reescrito.

## 11. Máquina de estados

```text
pending -> processing -> succeeded
                      -> failed_permanent
                      -> retry_scheduled -> processing
                      -> dead_letter

failed_permanent -- replay autorizado --> pending
dead_letter      -- replay autorizado --> pending
```

Transições são implementadas como updates condicionais no banco; não como read-then-write solto. `cancelled` permanece reservado e não pode ser persistido no MVP. Attempts são append-only; somente a linha ainda `started` pode receber seus campos finais uma vez, vinculada ao fencing token.

## 12. Protocolo HMAC

Headers de cada tentativa:

```text
WDE-Signature-Version: v1
WDE-Timestamp: <Unix seconds UTC>
WDE-Event-ID: <opaque event id>
WDE-Delivery-ID: <opaque delivery id>
WDE-Signature: key_id=<kid>,v1=<base64url>; key_id=<kid-antigo>,v1=<base64url>
Content-Type: application/json
```

Para cada `key_id`, a entrada canônica é a concatenação byte a byte:

```text
v1\n<timestamp>\n<event_id>\n<delivery_id>\n<key_id>\n<body bruto>
```

O HMAC é SHA-256 e a codificação é base64url sem padding. O corpo não é normalizado ou serializado novamente entre assinatura e envio. Alterar versão, timestamp, IDs, key ID ou corpo invalida a assinatura.

O receptor de referência aceita desvio padrão de ±5 min, configurável até no máximo ±15 min, compara em tempo constante e deduplica pelo `delivery_id`. HMAC não promete entrega única.

Rotação cria uma nova versão ativa e torna a anterior `retiring`. Durante 24 h por padrão (máximo 7 dias), o worker assina com ambas as versões em ordem nova/antiga. A seleção de versões ocorre atomicamente no início de cada tentativa; retries posteriores seguem o keyring vigente naquele instante. Após a janela, a versão anterior não assina e seu ciphertext é expurgado quando nenhuma operação ativa o referencia. Algoritmo ou versão desconhecidos falham fechado.

## 13. Cliente HTTP e defesa SSRF

### 13.1 Cadastro e canonicalização

- somente URL absoluta HTTPS fora do profile local;
- rejeitar userinfo, fragmento, host vazio, porta inválida, controles, zona IPv6 e representação ambígua;
- normalizar hostname, ponto final e IDN para ASCII antes de persistir e comparar;
- bloquear IP literal ou DNS que contenha qualquer endereço loopback, privado, link-local, multicast, unspecified, reserved, documentation/benchmark e metadata, em IPv4, IPv6 e IPv4-mapped IPv6;
- falhar fechado se qualquer A/AAAA retornado for proibido.

A validação no cadastro melhora feedback, mas nunca substitui a validação por tentativa.

### 13.2 Resolução e conexão

Para cada tentativa, `outboundhttp` usa resolver explícito com timeout de 2 s. Após validar todos os resultados, seleciona um IP permitido e entrega a `DialContext` um endereço construído com esse IP. O transporte não recebe o hostname para resolver novamente. `tls.Config.ServerName` mantém o hostname canonicalizado e a validação normal de cadeia/hostname; `InsecureSkipVerify` não é configurável.

`Proxy` é `nil`; `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY` são ignorados. Redirects retornam erro via `CheckRedirect`. Não há fallback para resolver do sistema depois de falha. A política é testada com DNS mutável para provar ausência de TOCTOU.

### 13.3 Limites de saída

- DNS: 2 s;
- conexão TCP: 3 s;
- TLS handshake: 5 s;
- response headers: 5 s;
- total da tentativa: 10 s por padrão, teto produtivo de 20 s;
- headers de resposta: limite do transporte e allowlist para metadados;
- corpo lido: no máximo 64 KiB para descarte controlado; preview opcional máximo 2 KiB;
- headers de resposta: no máximo 32 KiB via `MaxResponseHeaderBytes`;
- request headers: allowlist criada pelo serviço, sem propagação dos headers de entrada;
- conexão e body são sempre fechados, inclusive em erro/cancelamento.

Preview de resposta permanece desabilitado em todo o MVP. Se for habilitado no futuro, exigirá novo review, criptografia autenticada, allowlist de content type e retenção própria; sanitização heurística isolada não é considerada proteção suficiente.

## 14. Criptografia e gestão de segredos

HMAC secrets e payloads usam **AES-256-GCM** no nível da aplicação, com keyrings separados (`signing` e `payload`). Cada registro guarda versão da KEK, nonce aleatório, ciphertext e tag. O AAD inclui versão do formato, workspace, tipo e ID do recurso, impedindo transplante de ciphertext entre linhas/tenants.

As KEKs nunca ficam no PostgreSQL nem no repositório. Em produção são lidas de arquivos de secret montados somente no processo autorizado; variável de ambiente é permitida apenas no profile local. A API possui acesso ao keyring de payload para ingestão e ao caso de uso restrito de rotação; o worker possui acesso aos dois keyrings para entrega. Roles de banco continuam separadas.

Rotação de KEK usa versões: nova escrita utiliza a versão primária, leituras aceitam versões explicitamente configuradas e um job auditável reencripta gradualmente. Ciphertext adulterado, key version ausente ou AAD incorreto falha fechado. Plaintext vive somente no escopo do request/tentativa e jamais é formatado em erro.

API key não é recuperável e usa verificador; não é cifrada.

## 15. Retenção, purge e backups

Defaults e tetos do MVP:

| Categoria | Default | Máximo configurável | Após expiração |
|---|---:|---:|---|
| Payload cifrado e idempotência | 30 dias | 90 dias | ciphertext removido; replay impossível; metadado mínimo pode permanecer até 90 dias |
| Preview sanitizado de resposta | desabilitado | 2 KiB por 7 dias | removido |
| Attempts e metadados de delivery/event | 90 dias | 180 dias | removidos por lote respeitando referências |
| Auditoria de ações sensíveis | 365 dias | 730 dias | removida somente pelo purge administrativo |
| Buckets de rate limit expirados | 24 h | 7 dias | removidos |
| Backups cifrados | 35 dias | 35 dias no MVP | expiram; exclusão imediata de backup imutável não é prometida |

O purge roda no worker a cada hora, em batches pequenos, é idempotente e publica contagem/duração/idade do item mais antigo, nunca conteúdo. SLA: dado vencido sai da base ativa em até 24 h. Falha contínua torna readiness operacional degradada e gera alerta, sem derrubar liveness.

Exclusão de workspace primeiro revoga chaves e desabilita endpoints em transação, impedindo novos envios; depois agenda purge completo. Restauração de backup exige reconciliar revogações e tombstones externos ao snapshot antes de liberar tráfego. Detalhes físicos e constraints serão fechados pelo Data Architect.

## 16. Quotas, rate limits e backpressure

Defaults iniciais, todos configuráveis dentro de tetos seguros:

| Dimensão | Limite default |
|---|---:|
| Chaves ativas por workspace | 10 |
| Endpoints ativos por workspace | 100 |
| Fan-out por evento | 100 |
| Payload | 1 MiB |
| Idempotency-Key | 128 bytes |
| Event type | 128 bytes |
| Ingestão por workspace | 100 req/s, burst 200 |
| Mutação de endpoint/rotação | 60/min por workspace |
| Replay | 10/min por workspace e 2/min por delivery |
| Consultas | 300/min por workspace |
| Página | 50 default, 100 máximo |
| Concorrência worker | 32 por processo |
| Concorrência por endpoint | 4 globalmente no banco |

Há três camadas:

1. limites de servidor e semáforo global recusam excesso antes de alocar trabalho caro;
2. limiter local bounded-LRU reduz brute force por origem/prefixo sem criar cardinalidade no banco;
3. buckets atômicos no PostgreSQL impõem quotas documentadas para chaves/workspaces e ações sensíveis entre instâncias e reinícios.

O limiter local de origem é proteção best-effort por instância, documentada como tal; quotas autenticadas são persistentes e globais ao cluster PostgreSQL. Um workspace saturado tem buckets próprios e não consome todo o pool. Violações retornam `429` com `Retry-After` limitado ou erro de quota estável antes do commit, sem persistência parcial.

No worker, o número de claims é sempre menor ou igual à capacidade livre. O limite por endpoint é conferido sob lock de sua linha de capacidade no banco, valendo entre workers. A seleção limita itens por endpoint no batch para reduzir monopolização.

## 17. Persistência e transações

O Data Architect definirá nomes e DDL finais, preservando estes agregados conceituais:

- workspaces e tombstones de exclusão;
- API keys e escopos;
- endpoints, subscriptions e versões cifradas de segredo;
- events com fingerprint e payload cifrado;
- deliveries com estado, agenda, lease e fencing token;
- delivery attempts append-only;
- replay commands idempotentes;
- audit events append-only;
- rate-limit buckets e jobs de purge.

Regras:

- toda PK pública é opaca e não sequencial;
- toda relação tenant-owned carrega `workspace_id` e usa constraints compostas;
- timestamps vêm do servidor/banco em UTC;
- transações são abertas no adapter por uma abstração estreita de unit of work, não nos handlers;
- chamadas HTTP, DNS, exportação de telemetria e espera nunca ocorrem dentro de transação;
- isolamento padrão `READ COMMITTED`; usar serialização/locks somente na invariável que exigir;
- queries públicas têm timeout; migrations usam lock e timeout explícitos;
- attempts/audit não têm API de update/delete; finalização de attempt é operação monotônica condicionada.

Pools são separados e limitados. `api` e `worker` usam usuários de banco diferentes com mínimo privilégio; migration role não é usada em runtime. O worker não emite/revoga API keys; a API não executa claim/finalização de worker.

## 18. Configuração

Configuração é uma struct tipada, carregada uma vez no startup com precedência:

```text
flags não secretas > variáveis WDE_* não secretas > arquivo de configuração > defaults seguros
secret files montados > variável local explicitamente permitida
```

O profile é enum (`local`, `test`, `production`). Em `production`, startup falha se TLS de entrada exigido pela topologia não estiver declarado, HTTP local estiver habilitado, keyrings/pepper faltarem, DSN estiver ausente, limites estiverem fora de faixa ou profiling estiver ativo. Não existe fallback para chave hardcoded.

Arquivos com pepper, KEKs ou credenciais recusam symlink e permissões de grupo/outros; devem pertencer ao UID do processo e ter modo máximo `0600`. Conexão PostgreSQL por TCP em produção exige TLS com verificação de cadeia e hostname (`verify-full` ou equivalente); socket Unix local deve ser declarado explicitamente. Modos `disable`, `allow` e `prefer` são rejeitados em produção.

O log de startup informa somente nome da opção, fonte e valor quando não sensível. Nunca imprime DSN, key material, headers, hostname completo com query ou configuração inteira. Alterações exigem restart no MVP; hot reload fica adiado.

## 19. Erros e contratos internos

- erros de domínio possuem códigos estáveis (`not_found`, `conflict`, `invalid_transition`, `quota_exceeded`), sem texto de infraestrutura;
- adapters envolvem erro com contexto operacional seguro usando `%w`;
- handlers usam `errors.Is/As` e uma única tabela de mapeamento para problem+json;
- erros inesperados viram `500 internal_error` com `request_id`, sem SQL/stack/host;
- valores não confiáveis não são concatenados a logs; campos passam por allowlist e truncamento;
- contexto cancelado/deadline é preservado para shutdown e classificação;
- panic recovery existe somente na fronteira HTTP/goroutine owner e não tenta continuar estado desconhecido.

## 20. Observabilidade

### 20.1 Logs

`slog` JSON com campos permitidos: serviço, versão, ambiente, severity, request/trace IDs, IDs opacos de evento/delivery/attempt, status/código, duração e contagens. Workspace/endpoint podem ser correlacionados apenas por token derivado estável e não reversível quando necessário. Proibidos: payload, API key, HMAC, assinatura, headers, query, URL completa, DSN, ciphertext e corpo externo.

### 20.2 Métricas

Exemplos:

- `wde_http_requests_total{route,method,status_class}`;
- `wde_event_ingest_duration_seconds`;
- `wde_delivery_attempts_total{outcome,category}`;
- `wde_delivery_duration_seconds{outcome}`;
- `wde_queue_ready` e `wde_queue_oldest_seconds`;
- `wde_active_workers`, `wde_inflight_deliveries`;
- `wde_fencing_rejections_total`;
- `wde_rate_limit_rejections_total{operation}`;
- `wde_purge_items_total{category}` e `wde_purge_lag_seconds`.

Nunca usar workspace, key, IP, URL, endpoint, evento, delivery ou erro bruto como label.

### 20.3 Traces

Spans cobrem ingresso, transação, claim, DNS, conexão, tentativa e finalização. Atributos seguem convenções OTel e allowlist; conteúdo sensível e URL completa são omitidos. Export falha sem interromper o caminho de negócio e usa TLS/autenticação fora de local.

### 20.4 Probes e profiling

- liveness: processo/event loop, sem consultar destino nem banco;
- readiness API: listener pronto, PostgreSQL utilizável e migrations compatíveis;
- readiness worker: PostgreSQL/keyrings válidos e scheduler aceitando claims;
- listener operacional separado em loopback/rede interna, respostas mínimas;
- `pprof` não é compilado/registrado por padrão em produção; habilitação local exige flag explícita.

## 21. Estratégia de testes

### 21.1 Pirâmide

- unitários table-driven para validação, estados, backoff, HMAC, canonicalização, quotas e redaction;
- property/fuzz tests para parser de URL, IP, headers, problem details e idempotência;
- integração com PostgreSQL real via Testcontainers para constraints, transactions, SKIP LOCKED e roles;
- integração HTTP com `httptest`/servidores TLS para timeout, 4xx/5xx, limites e HMAC;
- adversariais para tenancy, SSRF, DNS rebinding, redirects, TLS e canários de vazamento;
- end-to-end com Compose para crash/restart, múltiplos workers, graceful shutdown e purge;
- benchmarks com ambiente, hardware, dados, duração e limites publicados.

### 21.2 Gates obrigatórios

- 100 requests concorrentes iguais criam um evento lógico;
- chave igual/conteúdo diferente retorna conflito;
- dois tenants não acessam recursos um do outro em nenhuma rota;
- worker obsoleto nunca finaliza com token antigo ou lease expirado;
- crash recupera até TTL + 5 s;
- goroutines/memória ficam limitadas com destino bloqueado;
- suite SSRF cobre IPv4/IPv6, mapped IP, IDN, rebinding, proxy, redirect e TLS;
- vetores HMAC independentes cobrem adulteração, clock skew e rotação;
- payload/segredo canário não aparece em nenhum sink;
- purge controlado por relógio impede replay depois do expurgo;
- `go test -race ./...` passa.

Testes dependentes de tempo recebem `Clock` mínima definida pelo consumidor; produção usa relógio real. Não criar abstração de relógio onde `testing/synctest` resolver com menor complexidade.

## 22. CI, supply chain e artefatos

Pipeline em PR:

1. verificar `gofmt`, `go mod tidy` e artefatos `sqlc` sem diff;
2. `go test ./...` e suítes de integração;
3. `go test -race ./...` em job próprio;
4. `go vet`, `go tool staticcheck`, `go tool govulncheck`;
5. lint do OpenAPI e migrations;
6. scanner de segredo;
7. build dos três binários e imagens;
8. smoke test Compose;
9. gerar SBOM e escanear exatamente as imagens produzidas.

Ações de CI usam SHA imutável. Dependências e tools são pinadas. High/Critical bloqueia release, salvo exceção versionada com finding, justificativa, responsável, mitigação e expiração. PR de fork não recebe secrets.

Imagens são multi-stage, finais mínimas/distroless compatíveis, executam como UID não-root, sem shell/toolchain/fonte/`.git`, com filesystem read-only e volume temporário explícito se necessário. Artefatos incluem versão, commit e SBOM; nenhuma build argument contém segredo.

## 23. Containers e execução local

Compose oferece profiles:

- `core`: PostgreSQL, API e worker;
- `demo`: adiciona Chaos Lab e observabilidade local;
- `test`: dependências para integração/E2E.

Migrations rodam como job explícito antes da readiness, usando role separada; runtime não migra automaticamente. Healthchecks não contêm credenciais. Dados sintéticos e certificados locais ficam claramente separados; secrets reais e `.env` não são versionados.

O Chaos Lab faz bind somente em loopback por padrão, não compartilha imagem com API/worker e é rejeitado quando `WDE_PROFILE=production`.

## 24. Threat model e controles por requisito

| Requisitos | Decisão arquitetural |
|---|---|
| SEC-001–004 | Principal derivado da key; queries tenant-scoped; verifier HMAC; revogação imediata; limiter local + quota persistente |
| SEC-005–007 | Canonical HMAC v1, janela ±5 min, IDs estáveis e assinatura dupla em overlap |
| SEC-008–011 | AES-256-GCM com KEKs fora do DB, retenção concreta, purge e workflow de exclusão/backups |
| SEC-012–015 | Parser estrito, todos IPs validados, DialContext fixado, proxy/redirect off, TLS e deadlines |
| SEC-016–018 | Quotas PostgreSQL, fan-out 100, cursor limitado, replay idempotente e atômico |
| SEC-019–022 | Auditoria append-only, telemetria allowlist, listener operacional separado e startup fail-closed |
| SEC-023–025 | Toolchain/actions pinadas, scanners/SBOM, imagem não-root e runbook antes de dados de terceiros |

Defesas de infraestrutura recomendadas, sem substituir controles do código: egress firewall negando redes internas/metadata, TLS no proxy de entrada, rede privada para PostgreSQL, backups cifrados e secret manager. A ausência dessas defesas restringe o ambiente a dados sintéticos.

## 25. Decisões adiadas explicitamente

| Decisão adiada | Gatilho para revisitar |
|---|---|
| Redis/Kafka ou broker | benchmark/produção demonstrar contenção ou retenção que PostgreSQL não atende |
| Kubernetes/autoscaling | múltiplos hosts e SLO operacional real |
| RLS obrigatória | Data Architect e security review avaliarem compatibilidade com roles/worker; filtro explícito permanece |
| Circuit breaker adaptativo | métricas mostrarem retries/backoff insuficientes |
| `LISTEN/NOTIFY` | polling gerar latência/carga relevante |
| Extensão periódica de lease | tentativas legítimas precisarem exceder a relação timeout/TTL atual |
| KMS/HSM e envelope com DEK por registro | hospedagem real, requisito de compliance ou rotação em escala |
| Multi-região/HA | demanda real e definição de RPO/RTO |
| Dashboard, CLI e login humano | contrato API estabilizado e feedback justificar |
| Payload retrieval | caso de uso e novo security review; proibido no MVP |
| Redirect controlado | necessidade real e novo threat model; desabilitado no MVP |

Adiamento não autoriza stub inseguro ou flag oculta. Recursos ausentes retornam comportamento explícito.

## 26. Evolução e critérios de extração

A API e o worker já são processos separados para escalarem independentemente, mas continuam um produto e codebase. Um módulo só vira serviço se métricas mostrarem necessidade de isolamento de deploy, escala ou risco, e após definir propriedade de dados, contrato e falhas. Compartilhar PostgreSQL entre serviços extraídos não será tratado como separação concluída.

Possíveis evoluções, na ordem: hardening após review, dashboard/CLI, circuit breaker, coordenação de quotas em escala maior, KMS, broker e orquestração. Nenhuma entra no MVP por valor de currículo apenas.

## 27. ADRs

- ADR-001 — Monólito modular com binários API, worker e Chaos Lab.
- ADR-002 — PostgreSQL como fonte de verdade e fila com `SKIP LOCKED`.
- ADR-003 — Semântica `at-least-once` e idempotência.
- ADR-004 — Leases e fencing para coordenação de workers.
- ADR-005 — Stack HTTP, persistência, migrações e ferramentas.
- ADR-006 — Protocolo HMAC versionado e rotação.
- ADR-007 — Cliente HTTP com defesa SSRF no `DialContext`.
- ADR-008 — Observabilidade segura e superfície operacional separada.
- ADR-009 — Criptografia e retenção de payloads e segredos.
- ADR-010 — Tenancy, API keys e quotas persistentes.

## 28. Gate para implementação

Nenhum código de produção deve começar antes de:

- Security Review desta arquitetura;
- arquitetura de dados definir schema, constraints, índices, papéis e migrations;
- Security Review do schema;
- OpenAPI inicial alinhar estados, problem details e protocolo HMAC;
- backlog rastrear `FR-*`, `NFR-*` e `SEC-001`–`SEC-025`;
- `AGENTS.md` ser respeitado por todas as sessões.

Depois dos gates, o primeiro corte vertical será: criar endpoint local seguro, publicar evento idempotente, persistir evento+delivery, worker com concorrência 1 entregar HMAC ao Chaos Lab e consultar o resultado. Leases, retries, quotas e hardening entram em cortes seguintes sem alterar as invariantes desta fundação.
