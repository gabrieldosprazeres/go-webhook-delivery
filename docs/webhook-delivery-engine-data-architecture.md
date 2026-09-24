# Arquitetura de Dados: Webhook Delivery Engine

**Status:** Remediado após Security Review do schema; aguardando reaprovação  
**Versão:** 1.1  
**Data:** 2026-09-23  
**Banco:** PostgreSQL 17  
**Fontes:** PRD, arquitetura, ADR-002/003/004/009/010 e requisitos `SEC-001`–`SEC-025`

## 1. Objetivos e invariantes

O PostgreSQL é simultaneamente fonte de verdade e fila durável. O schema precisa preservar, mesmo diante de concorrência e bugs de aplicação:

1. toda linha de negócio pertence a um `workspace_id`;
2. relações tenant-owned usam FKs compostas contendo `workspace_id`;
3. evento e todas as deliveries elegíveis são criados na mesma transação;
4. idempotência é única por workspace e compara fingerprint versionado;
5. claim, finalização e replay são updates condicionais, nunca read-then-write solto;
6. fencing token cresce monotonicamente e finalização obsoleta afeta zero linhas;
7. histórico de tentativas e auditoria não pode ser apagado/reescrito pelas roles de runtime;
8. payloads e segredos recuperáveis ficam apenas como ciphertext autenticado;
9. o relógio autoritativo é o PostgreSQL em UTC;
10. API, worker, administração e migrations possuem roles e privilégios distintos.

## 2. Decisões de modelagem

- IDs públicos são UUIDv7 gerados em Go usando CSPRNG e armazenados como `uuid`. PostgreSQL 17 não é responsável pela geração; a aplicação valida versão/variante. UUIDv7 melhora localidade sem expor sequência simples.
- Estados usam `text` com `CHECK`, evitando a rigidez operacional de enums PostgreSQL em migrations.
- Dinheiro, JSON transformável, blobs externos e IDs seriais não existem no MVP.
- `timestamptz` é obrigatório; writes usam `clock_timestamp()` ou `statement_timestamp()` conforme indicado.
- Idempotency keys, fingerprints de conteúdo e dimensões sensíveis de rate limit são armazenadas como HMAC-SHA-256 com peppers independentes, nunca em texto aberto.
- Todo envelope AES-256-GCM registra `cipher_format_version`, `kek_version`, nonce de 12 bytes e ciphertext com tag; todos os campos usam constraint all-or-none.
- Nenhum trigger de negócio será usado. Integridade depende de constraints, grants e statements condicionais visíveis/testáveis.

## 3. ERD textual

```text
workspaces
  ├── api_keys ──< api_key_scopes
  ├── endpoints ──< endpoint_subscriptions
  │       ├── endpoint_secret_versions
  │       └── endpoint_runtime
  ├── events ──< deliveries ──< delivery_attempts
  │                   └── replay_commands
  ├── audit_events
  ├── rate_limit_buckets
  └── maintenance_jobs

workspace_tombstones mantém revogação/exclusão após a remoção ativa.
```

Toda seta entre recursos de tenant representa FK composta `(workspace_id, id)`.

## 4. Tabelas

### 4.1 `workspaces`

| Coluna | Tipo | Regras |
|---|---|---|
| `id` | `uuid` | PK, UUIDv7 |
| `name` | `text` | 1–120 caracteres |
| `status` | `text` | `active`, `suspended`, `deleting` |
| `payload_retention_days` | `smallint` | 1–90, default 30 |
| `metadata_retention_days` | `smallint` | 30–180, default 90 |
| `created_at` | `timestamptz` | default `clock_timestamp()` |
| `updated_at` | `timestamptz` | atualizado explicitamente |
| `deletion_requested_at` | `timestamptz` | nullable |

Constraints: PK `(id)` e `UNIQUE (id, status)` apenas quando necessário a comandos administrativos; relações usam o ID do workspace diretamente.

### 4.2 `workspace_tombstones`

| Coluna | Tipo | Regras |
|---|---|---|
| `workspace_id` | `uuid` | PK; não referencia workspace removido |
| `generation` | `bigint` | positivo |
| `revoked_at` | `timestamptz` | obrigatório |
| `purge_completed_at` | `timestamptz` | nullable |
| `backup_expiry_at` | `timestamptz` | obrigatório |

O tombstone é conciliado após restore antes de liberar tráfego e impede reativação acidental.

### 4.3 `api_keys`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id` | `uuid` | PK `id`; unique `(workspace_id,id)` |
| `prefix` | `text` | unique global, 8–32 chars, não secreto |
| `verifier` | `bytea` | exatamente 32 bytes |
| `status` | `text` | `active`, `revoked` |
| `expires_at`, `revoked_at` | `timestamptz` | nullable e coerentes com estado |
| `created_at`, `last_used_at` | `timestamptz` | `last_used_at` nullable |

`api_key_scopes(workspace_id, api_key_id, scope)` possui PK nas três colunas, FK composta para `api_keys` e `CHECK` limitando `events:write`, `endpoints:write`, `deliveries:read`, `deliveries:retry` e `admin`.

A role da API não recebe `SELECT` direto nessas tabelas antes da autenticação. Ela executa uma função `SECURITY DEFINER authenticate_key(prefix)` com `search_path` fixo, que retorna apenas ID, workspace, verifier, status, expiração e scopes. Comparação criptográfica permanece no Go, incluindo verifier dummy para prefixo inexistente.

### 4.4 `endpoints`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id` | `uuid` | PK e unique composta |
| `status` | `text` | `active`, `paused`, `disabled`, `deleting` |
| `scheme` | `text` | `https`; `http` somente profile local e não persistido em produção |
| `host_ascii` | `text` | canonicalizado, 1–253 chars |
| `port` | `integer` | 1–65535 |
| `target_cipher_format_version` | `smallint` | versão conhecida e positiva |
| `target_ciphertext` | `bytea` | path + query cifrados, mínimo 16 bytes de tag |
| `target_nonce` | `bytea` | 12 bytes |
| `target_kek_version` | `smallint` | positivo |
| `max_concurrency` | `smallint` | 1–32, default 4 |
| `created_at`, `updated_at` | `timestamptz` | obrigatórios |

`endpoint_subscriptions(workspace_id, endpoint_id, event_type)` possui PK composta; `event_type` tem 1–128 caracteres e formato limitado.

`endpoint_runtime(workspace_id, endpoint_id, lock_version, updated_at)` possui uma linha por endpoint. Sua linha é bloqueada durante claims para serializar o cálculo de concorrência global sem contador suscetível a vazamento após crash.

### 4.5 `endpoint_secret_versions`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id`, `endpoint_id` | `uuid` | PK e FK composta |
| `key_id` | `text` | identificador público único por endpoint |
| `state` | `text` | `active`, `retiring`, `retired`, `purged` |
| `cipher_format_version` | `smallint` | versão conhecida e positiva |
| `secret_ciphertext` | `bytea` | nullable somente quando `purged`, mínimo 16 bytes |
| `secret_nonce` | `bytea` | 12 bytes enquanto ciphertext existir |
| `kek_version` | `smallint` | positivo enquanto ciphertext existir |
| `valid_from`, `retire_at`, `purged_at`, `created_at` | `timestamptz` | coerência por `CHECK` |

Índice unique parcial garante uma única versão `active` por endpoint. Outro índice cobre versões `retiring` ainda válidas. Rotação bloqueia a linha do endpoint, promove a nova versão e define expiração da anterior na mesma transação.

### 4.6 `events`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id` | `uuid` | PK e unique composta |
| `idempotency_key_hash` | `bytea` | 32 bytes |
| `idempotency_fingerprint` | `bytea` | 32 bytes |
| `fingerprint_version` | `smallint` | começa em 1 |
| `event_type` | `text` | 1–128 chars |
| `payload_cipher_format_version` | `smallint` | versão conhecida e positiva |
| `payload_ciphertext` | `bytea` | nullable depois de purge, mínimo 16 bytes |
| `payload_nonce` | `bytea` | 12 bytes enquanto ciphertext existir |
| `payload_kek_version` | `smallint` | positivo enquanto ciphertext existir |
| `payload_size` | `integer` | 0–1 MiB |
| `payload_expires_at`, `payload_purged_at` | `timestamptz` | coerentes |
| `created_at` | `timestamptz` | obrigatório |

Unique `(workspace_id, idempotency_key_hash)`. A chave e o fingerprint são HMAC-SHA-256 com peppers próprios e separados entre si e do pepper de autenticação. Ciphertext, formato, nonce e versão devem estar todos presentes ou todos ausentes.

AAD canônico é uma sequência binária length-prefixed, nunca concatenação ambígua:

- endpoint target: `format | workspace_id | endpoint_id | scheme | host_ascii | port`;
- endpoint secret: `format | workspace_id | endpoint_id | secret_version_id | key_id`;
- event payload: `format | workspace_id | event_id | event_type`.

Reencriptação sempre gera nonce novo. Alteração em qualquer metadado coberto faz a descriptografia falhar fechado.

### 4.7 `deliveries`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id`, `event_id`, `endpoint_id` | `uuid` | FKs compostas |
| `status` | `text` | `pending`, `processing`, `retry_scheduled`, `succeeded`, `failed_permanent`, `dead_letter` |
| `run_number` | `integer` | começa em 1 e cresce somente por replay válido |
| `attempts_in_run` | `smallint` | 0–100; zera em novo run |
| `attempt_sequence` | `integer` | total monotônico durante toda a vida |
| `max_attempts_per_run` | `smallint` | 1–100; default 10 |
| `next_attempt_at` | `timestamptz` | obrigatório em estados elegíveis |
| `lease_owner` | `uuid` | nullable; identifica instância worker |
| `lease_expires_at` | `timestamptz` | nullable |
| `fencing_token` | `bigint` | default 0, não negativo |
| `last_error_category` | `text` | nullable, vocabulário limitado na aplicação |
| `succeeded_at`, `terminal_at` | `timestamptz` | nullable e coerentes |
| `created_at`, `updated_at` | `timestamptz` | obrigatórios |

Constraints impedem lease em estado não `processing`, sucesso sem `succeeded_at`, terminal incompleto, `attempts_in_run > max_attempts_per_run` e contadores negativos. `run_number >= 1`; `attempt_sequence >= attempts_in_run`. Unique `(workspace_id,id)` sustenta todas as relações tenant-scoped.

Índices:

```sql
CREATE INDEX deliveries_ready_idx
  ON deliveries (next_attempt_at, created_at, id)
  WHERE status IN ('pending', 'retry_scheduled');

CREATE INDEX deliveries_expired_lease_idx
  ON deliveries (lease_expires_at, id)
  WHERE status = 'processing';

CREATE INDEX deliveries_workspace_list_idx
  ON deliveries (workspace_id, created_at DESC, id DESC);

CREATE INDEX deliveries_endpoint_active_idx
  ON deliveries (workspace_id, endpoint_id, lease_expires_at)
  WHERE status = 'processing';
```

### 4.8 `delivery_attempts`

| Coluna | Tipo | Regras |
|---|---|---|
| `id`, `workspace_id`, `delivery_id` | `uuid` | FK composta |
| `run_number` | `integer` | run vigente, positivo |
| `attempt_number_in_run` | `smallint` | positivo e limitado pelo run |
| `attempt_sequence` | `integer` | monotônico por delivery |
| `fencing_token` | `bigint` | token do claim |
| `state` | `text` | `started`, `completed`, `abandoned` |
| `outcome` | `text` | nullable até conclusão; `success`, `retry`, `permanent_failure`, `stale` |
| `http_status` | `smallint` | nullable, 100–599 |
| `duration_ms`, `response_bytes_read` | `integer` | não negativos |
| `error_category` | `text` | nullable e sanitizado |
| `started_at`, `finished_at` | `timestamptz` | coerentes |

Unique `(workspace_id, delivery_id, run_number, attempt_number_in_run)`, `(workspace_id, delivery_id, attempt_sequence)` e `(workspace_id, delivery_id, fencing_token)`. Nenhum corpo/header de resposta é armazenado. Runtime não recebe DML direto; funções controladas aceitam apenas `started → completed|abandoned` uma vez.

### 4.9 `replay_commands`

Armazena `id`, `workspace_id`, `delivery_id`, snapshot opaco do ator, `idempotency_key_hash`, `fingerprint`, `fingerprint_version`, `new_run_number`, `reason`, `request_id`, `result`, `created_at`. Unique `(workspace_id,idempotency_key_hash)`. Motivo tem 1–500 caracteres, é sanitizado e nunca contém payload automaticamente. O snapshot do ator sobrevive ao purge da API key sem FK/cascade.

### 4.10 `audit_events`

Armazena `id`, `workspace_id` nullable somente para bootstrap, `actor_type`, `actor_id`, `action`, `resource_type`, `resource_id`, `request_id`, `outcome`, `reason`, `created_at`. É append-only; roles de runtime recebem apenas inserção por interface/função e leitura tenant-scoped quando futuramente exposta. Índices por `(workspace_id, created_at DESC, id DESC)` e por retenção.

### 4.11 `rate_limit_buckets`

PK `(dimension_hash, operation, window_started_at)`, com `workspace_id`/`api_key_id` opcionais conforme dimensão, `count`, `expires_at`. `dimension_hash` é HMAC; IP, token e prefixo brutos não são persistidos. Incremento é `INSERT ... ON CONFLICT ... DO UPDATE SET count = count + 1 ... RETURNING count`.

### 4.12 `maintenance_jobs`

Armazena jobs idempotentes de `purge_payload`, `purge_workspace`, `reencrypt_payload`, `reencrypt_secret` e `reconcile_tombstone`: ID, workspace nullable, tipo, chave de deduplicação, status, agenda, lease/fencing, tentativas e timestamps. Usa as mesmas garantias de claim, mas nunca compartilha estado com delivery.

### 4.13 Contrato exato de relações

Toda tabela com ID próprio declara PK `(id)` e `UNIQUE (workspace_id,id)`. As seguintes FKs são obrigatórias e usam `ON UPDATE RESTRICT ON DELETE RESTRICT` durante a vida operacional:

```text
api_keys.workspace_id                         -> workspaces.id
api_key_scopes.(workspace_id,api_key_id)      -> api_keys.(workspace_id,id)
endpoints.workspace_id                        -> workspaces.id
endpoint_subscriptions.(workspace_id,endpoint_id)
                                              -> endpoints.(workspace_id,id)
endpoint_runtime.(workspace_id,endpoint_id)   -> endpoints.(workspace_id,id)
endpoint_secret_versions.(workspace_id,endpoint_id)
                                              -> endpoints.(workspace_id,id)
events.workspace_id                           -> workspaces.id
deliveries.(workspace_id,event_id)            -> events.(workspace_id,id)
deliveries.(workspace_id,endpoint_id)         -> endpoints.(workspace_id,id)
delivery_attempts.(workspace_id,delivery_id)  -> deliveries.(workspace_id,id)
replay_commands.(workspace_id,delivery_id)    -> deliveries.(workspace_id,id)
```

Auditoria armazena snapshots opacos de ator/recurso e não possui FK para entidade expurgável. `rate_limit_buckets` possui `CHECK` por tipo de dimensão: workspace exige apenas workspace, api_key exige par workspace+key, origin exige ambos nulos. `maintenance_jobs` possui `CHECK` por tipo: jobs tenant-scoped exigem workspace; jobs globais proíbem; jobs de recurso exigem tipo/ID correspondente. Pares opcionais usam `MATCH FULL`.

Purge administrativo remove explicitamente na ordem: bloquear/revogar → attempts/replay operacional vencido → deliveries → events → secrets/subscriptions/runtime → endpoints → scopes/keys → workspace. Auditoria e tombstone sobrevivem conforme suas retenções; nenhuma cascade apaga evidência histórica.

## 5. Ingestão idempotente

Dentro de uma única transação `READ COMMITTED`:

1. derivar `workspace_id` do principal e definir `SET LOCAL wde.workspace_id`;
2. validar workspace ativo, quotas e fan-out máximo;
3. tentar inserir `events` com `(workspace_id,idempotency_key_hash)`;
4. em conflito, bloquear/ler o evento existente;
5. fingerprint igual retorna o ID original sem criar deliveries; diferente retorna conflito;
6. para evento novo, selecionar endpoints ativos e subscriptions no mesmo workspace;
7. inserir todas as deliveries;
8. commit;
9. somente depois responder `202`.

Nenhum tratamento genérico de erro pode transformar falha/timeout de commit em `202`.

## 6. Claim e fencing

Claim ocorre em batch no máximo igual aos slots livres do worker e aplica tetos por workspace e endpoint em cada ciclo. Candidatos são intercalados deterministicamente por workspace; um backlog saturado não pode preencher sozinho o batch. Para cada endpoint candidato:

```sql
BEGIN;
SELECT 1
FROM endpoint_runtime
WHERE workspace_id = $workspace AND endpoint_id = $endpoint
FOR UPDATE;

-- Contagem considera apenas leases ainda válidos.
SELECT count(*)
FROM deliveries
WHERE workspace_id = $workspace
  AND endpoint_id = $endpoint
  AND status = 'processing'
  AND lease_expires_at > clock_timestamp();

-- Dentro da capacidade restante:
WITH candidate AS (
  SELECT id
  FROM deliveries
  WHERE workspace_id = $workspace
    AND endpoint_id = $endpoint
    AND (
      (status IN ('pending','retry_scheduled') AND next_attempt_at <= clock_timestamp())
      OR (status = 'processing' AND lease_expires_at <= clock_timestamp())
    )
  ORDER BY next_attempt_at, created_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT $capacity
)
UPDATE deliveries d
SET status = 'processing',
    lease_owner = $worker_id,
    lease_expires_at = clock_timestamp() + $lease_ttl,
    fencing_token = d.fencing_token + 1,
    attempts_in_run = d.attempts_in_run + 1,
    attempt_sequence = d.attempt_sequence + 1,
    updated_at = clock_timestamp()
FROM candidate c
WHERE d.id = c.id
RETURNING d.*;
COMMIT;
```

A função privilegiada de claim, na mesma transação e sob lock da delivery:

1. ao encontrar lease expirado, finaliza o attempt `started` do token anterior como `abandoned`;
2. se `attempts_in_run >= max_attempts_per_run`, leva a delivery a `dead_letter` sem criar chamada HTTP;
3. caso contrário incrementa token e contadores e cria exatamente um attempt `started` com `run_number`, `attempt_number_in_run`, `attempt_sequence` e novo token;
4. retorna somente o snapshot mínimo necessário à entrega.

O predicado do claim inclui `attempts_in_run < max_attempts_per_run`; o ramo de expiração/esgotamento é tratado antes do novo claim. Implementação usa função/CTE única e testada para impedir claim sem attempt ou `max + 1` após crashes repetidos.

Finalização:

```sql
UPDATE deliveries
SET status = $new_status,
    next_attempt_at = $next_attempt_at,
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_category = $category,
    succeeded_at = $succeeded_at,
    terminal_at = $terminal_at,
    updated_at = clock_timestamp()
WHERE workspace_id = $workspace
  AND id = $delivery
  AND status = 'processing'
  AND lease_owner = $worker
  AND fencing_token = $token
  AND lease_expires_at > clock_timestamp();
```

Zero rows significa resultado obsoleto. A finalização da delivery e somente do attempt `started` com o mesmo `(workspace, delivery, run_number, fencing_token)` ocorre na mesma transação. Nenhum caminho altera identidade, início ou outcome já finalizado. Resultado stale não reabre nem reescreve histórico.

## 7. Replay

Replay bloqueia a delivery, exige tenant, estado permitido e payload existente. Insere `replay_commands` de forma idempotente e compara `fingerprint_version + fingerprint` em conflito. Na mesma transação:

- incrementa `run_number`;
- zera somente `attempts_in_run`;
- preserva `attempt_sequence` e `fencing_token` monotônicos;
- grava o novo run no comando e na auditoria;
- altera a delivery para `pending` com lease vazio.

Attempts anteriores permanecem intactos. Delivery `succeeded`, payload purgado ou tenant divergente afeta zero linhas/retorna erro indistinguível. Ação sensível, comando e auditoria confirmam ou revertem juntos.

## 8. RLS e RBAC de banco

### 8.1 Roles

- `wde_owner NOLOGIN`: dona do schema; nunca usada pela aplicação.
- `wde_migrator LOGIN`: DDL controlado; sem uso em runtime.
- `wde_api LOGIN`: rotas públicas, dados tenant-scoped e funções permitidas.
- `wde_worker LOGIN`: claim/finalização/purge e leitura mínima para entrega; sem API keys.
- `wde_admin LOGIN`: comandos one-shot de credenciais/workspace; sem listener.
- `wde_readonly NOLOGIN`: diagnóstico local opcional, sem ciphertext; não concedida por padrão.
- `wde_auth_executor NOLOGIN`: owner somente da função de lookup pré-auth e policy de leitura limitada de credencial.
- `wde_worker_executor NOLOGIN`: owner somente das funções globais de claim/finalização/manutenção e policies necessárias.
- `wde_audit_executor NOLOGIN`: owner da função append-only de auditoria.

Nenhuma role runtime é superuser, owner ou possui `BYPASSRLS`, `CREATEROLE`, `CREATEDB` ou `CREATE` no schema.

### 8.2 RLS

RLS é habilitada e forçada (`ENABLE` + `FORCE ROW LEVEL SECURITY`) em todas as tabelas tenant-owned. Helper estável:

```sql
current_workspace_id() = NULLIF(current_setting('wde.workspace_id', true), '')::uuid
```

Políticas da API exigem `workspace_id = current_workspace_id()` para `USING` e `WITH CHECK`. Depois da autenticação, a aplicação inicia transação explícita e executa `SELECT set_config('wde.workspace_id', $1, true)` parametrizado na mesma conexão. Toda query tenant-scoped ocorre nessa transação; commit ou rollback é obrigatório antes de devolver a conexão ao pool. `SET` de sessão, interpolação de UUID e query tenant-scoped em autocommit são proibidos. Testes reutilizam a mesma conexão após commit, rollback, cancelamento e panic e comprovam que o tenant não vaza.

Qualquer role pode escolher um custom GUC; portanto, RLS reduz erros acidentais mas não protege contra comprometimento da credencial `wde_api`. Filtros explícitos, credenciais rotacionáveis, mínimo privilégio e monitoramento continuam obrigatórios.

O worker não recebe policy ampla nem acesso direto cross-tenant. Claims, finalizações, purge e manutenção passam obrigatoriamente por funções `SECURITY DEFINER` estreitas pertencentes a `wde_worker_executor`. O lookup pré-auth pertence a `wde_auth_executor`. Com `FORCE RLS`, policies `TO wde_auth_executor`/`TO wde_worker_executor` liberam apenas tabela, comando e predicado necessários.

Todas as funções privilegiadas usam `SECURITY DEFINER SET search_path = pg_catalog`, qualificam cada objeto com `wde.`, não usam SQL dinâmico, validam tamanho/formato de argumentos e têm `PUBLIC EXECUTE` revogado. Runtime recebe apenas `EXECUTE`, nunca membership/`SET ROLE` nas roles executoras. Lookup por prefixo retorna no máximo uma linha e comparação com pepper permanece no Go.

## 9. Matriz de owners, grants, policies e funções

| Objeto | Owner | Acesso runtime direto | Policy/função privilegiada |
|---|---|---|---|
| `api_keys`, `api_key_scopes` | `wde_owner` | nenhum pré-auth | policy SELECT somente `wde_auth_executor`; `authenticate_key(prefix)` |
| `workspaces`, endpoints, events, deliveries | `wde_owner` | `wde_api` apenas sob RLS tenant-scoped | `USING/WITH CHECK` para API; worker somente por funções executoras |
| `delivery_attempts` | `wde_owner` | nenhum DML | `claim_deliveries`, `finalize_delivery`, `expire_claim` |
| `audit_events` | `wde_owner` | nenhum DML | `append_audit_event` owned por `wde_audit_executor` |
| `replay_commands` | `wde_owner` | leitura tenant-scoped | `request_replay` executa comando + audit atomicamente |
| `rate_limit_buckets` | `wde_owner` | nenhum DML | `consume_quota` com dimensão allowlisted |
| `maintenance_jobs` | `wde_owner` | nenhum DML | funções de claim/finalização do executor worker |
| `workspace_tombstones`, estado de restore | `wde_owner` | nenhum acesso | comandos one-shot do `wde_admin` por função específica |

Regras globais:

- `PUBLIC` não possui `USAGE` no schema nem `EXECUTE` em funções.
- Runtime não recebe `TRUNCATE`, `REFERENCES`, `TRIGGER`, ownership ou DDL.
- API/worker/admin não recebem `INSERT/UPDATE/DELETE` direto em attempts ou auditoria.
- Ação sensível e audit event executam na mesma transação.
- Somente função de attempt pode preencher colunas finais uma vez; identidade/início/outcome finalizado não mudam.
- Ciphertext nunca é retornado por views/queries públicas.
- Testes consultam ACLs, owner, `prosecdef` e `proconfig`, e comprovam que DML direto falha.

## 10. Retenção e purge

- Payload: default 30, máximo 90 dias; purge zera ciphertext/nonce/key version e define `payload_purged_at`.
- Metadata event/delivery/attempt: default 90, máximo 180 dias, removida em batches após payload e sem referências ativas.
- Auditoria: default 365, máximo 730 dias.
- Rate-limit buckets: 24 h, máximo 7 dias.
- Tombstones e backups seguem 35 dias mínimos para reconciliação definida na arquitetura.

Jobs usam índice por data de expiração, `FOR UPDATE SKIP LOCKED`, batch pequeno e statement timeout. Purge é idempotente. Replay verifica payload antes de alterar estado.

Antes de purgar um payload, o job bloqueia o event e suas deliveries. Deliveries ainda ativas são aguardadas somente até o prazo documentado; depois, na mesma transação, o job incrementa o fencing token, encerra attempt `started` como `abandoned`, remove lease e terminaliza a delivery como `failed_permanent` com categoria `payload_expired`. Só então ciphertext, formato, nonce e versão são zerados juntos. Isso invalida workers atrasados e impede replay de conteúdo inexistente.

### 10.1 Restore fail-closed

O MVP não depende de tombstone contido no mesmo snapshot para proteger revogações posteriores. Todo restore entra obrigatoriamente em quarentena sem tráfego público ou egress. Antes da readiness:

1. comando administrativo one-shot revoga **todas** as API keys restauradas;
2. suspende todos os workspaces e desabilita todos os endpoints;
3. encerra leases e incrementa fencing tokens;
4. registra geração de restore e auditoria;
5. exige reemissão de credenciais e reativação explícita após reconciliação.

Essa escolha é conservadora e evita fonte externa adicional no MVP. Automação de restore recebe flag obrigatória de quarentena; promover snapshot sem executar o comando é falha operacional bloqueadora. O teste restaura backup anterior a uma revogação e prova `401` e ausência de entrega antes de liberar readiness. Em hospedagem real, um ledger externo append-only poderá substituir a revogação total mediante ADR e novo review.

### 10.2 Índices operacionais e fairness

Além dos índices de deliveries:

```text
endpoint_subscriptions (workspace_id, event_type, endpoint_id)
delivery_attempts       (workspace_id, delivery_id, attempt_sequence)
events                  (payload_expires_at, id) WHERE payload_ciphertext IS NOT NULL
audit_events            (created_at, id)
rate_limit_buckets      (expires_at)
maintenance_jobs        (status, scheduled_at, id)
replay_commands         (workspace_id, delivery_id, created_at DESC)
```

O scheduler seleciona no máximo um teto configurado por workspace e endpoint a cada ciclo, usa ranking particionado e alterna o cursor de workspace de forma determinística. Queries possuem `statement_timeout` na role, batch máximo e plano validado com `EXPLAIN (ANALYZE, BUFFERS)` sobre massa representativa. Teste de saturação exige progresso do tenant saudável enquanto outro mantém backlog máximo.

## 11. Migrations

Antes de qualquer tabela, o bootstrap configura `ALTER DEFAULT PRIVILEGES`, revoga `CREATE` em schemas acessíveis e revoga `PUBLIC EXECUTE` em funções. O migrator usa `SET ROLE wde_owner` para ownership determinístico. Cada migration cria tabela, grants mínimos, `ENABLE/FORCE RLS` e policy fail-closed na mesma transação; nunca existe commit intermediário com objeto aberto.

Ordem planejada:

1. `000001_roles_and_schema` — roles/bootstrap separado quando o provedor não permitir transaction/role DDL.
2. `000002_helpers_and_workspaces` — schema, helpers seguros, workspaces/tombstones.
3. `000003_api_keys` — credenciais, scopes e função pré-auth.
4. `000004_endpoints` — endpoints, subscriptions, runtime e secret versions.
5. `000005_events_and_deliveries` — events, deliveries, attempts e índices da fila.
6. `000006_operations` — replay, audit, rate limits e maintenance jobs.
7. `000007_privileged_operations` — funções executoras, policies específicas e grants `EXECUTE`.

Fixtures ficam fora da cadeia produtiva, em comando/pasta de teste separado com dupla trava: profile `local|test` e banco marcado como descartável. Migrations usam lock timeout e statement timeout explícitos. `down` existe para desenvolvimento quando reversível; migrations destrutivas em produção usam expand/contract e não dependem de rollback automático. Runtime nunca executa migrations. CI interrompe cada migration artificialmente e confirma que roles runtime continuam fail-closed.

## 12. Testes obrigatórios do schema

- mesmas queries com dois workspaces provam isolamento e `WITH CHECK`;
- conexão do pool sem `SET LOCAL` enxerga zero dados tenant-owned;
- FK composta rejeita event/endpoint/delivery cross-tenant;
- 100 inserts idempotentes concorrentes produzem um evento;
- claim concorrente não duplica versão e respeita concorrência por endpoint;
- lease expirado pode ser retomado; token antigo não finaliza;
- attempts/audit não podem ser apagados pelas roles runtime;
- worker não lê API keys; API não executa claim/finalização;
- ciphertext adulterado é rejeitado pela aplicação e metadados incoerentes falham em constraint;
- purge concorrente é idempotente e impede replay;
- cursor/filtro de tenant diferente não retorna dados;
- plano de `deliveries_ready_idx` é validado com massa representativa;
- migrations sobem do zero e upgrade da versão anterior preserva invariantes.

## 13. Riscos e decisões para implementação

- Funções `SECURITY DEFINER` são pequenas superfícies privilegiadas e exigem review dedicado, search path fixo e testes de grants.
- RLS não substitui filtros explícitos; queries continuam recebendo `workspace_id`.
- O lock por endpoint favorece correção e fairness, mas pode limitar hot endpoints; benchmark determinará se outra estratégia é necessária.
- UUIDv7 precisa de biblioteca pequena e pinada ou implementação revisada; não aceitar gerador pseudoaleatório.
- DDL final deve repetir os checks descritos aqui e será o objeto do Security Review do schema real antes do primeiro release.
