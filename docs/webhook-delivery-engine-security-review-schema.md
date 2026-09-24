# Security Review do Schema: Webhook Delivery Engine

**Status:** Aprovado condicionalmente após reavaliação da arquitetura de dados v1.1  
**Versão:** 1.0  
**Data:** 2026-09-23  
**Escopo:** `webhook-delivery-engine-data-architecture.md`, PRD, arquitetura e baseline `SEC-001`–`SEC-025`  
**Etapa:** Security Review do schema (passo 7)

## 1. Parecer executivo

A proposta tem uma base forte: tenant em todas as entidades de negócio, FKs compostas como intenção, RLS forçada, roles separadas, criptografia em nível de aplicação, fila transacional com lease/fencing e ausência de corpos externos persistidos. Porém, ainda há ambiguidades que mudam propriedades de segurança e concorrência. As mais graves são: o modelo de execução sob `FORCE RLS` não está fechado, replay não possui uma geração própria de tentativas, e o mecanismo de restore não preserva revogações posteriores ao snapshot.

O documento pode orientar a correção, mas ainda não deve virar DDL ou backlog de implementação sem resolver os achados abaixo.

## 2. Achados

### SCHEMA-SEC-001 — P0 — `FORCE RLS`, funções privilegiadas e pooling formam um modelo ainda inconsistente

Com `FORCE ROW LEVEL SECURITY`, uma função `SECURITY DEFINER` não atravessa RLS apenas por pertencer ao owner; o lookup pré-auth não possui tenant definido e o worker precisa operar entre tenants. A expressão “preferencialmente por funções” deixa duas implementações incompatíveis em aberto. Além disso, qualquer role PostgreSQL pode definir um custom GUC, portanto `wde.workspace_id` é proteção contra erro de query, não fronteira contra comprometimento da credencial `wde_api`.

**Impacto:** autenticação/claims podem falhar em produção ou uma implementação apressada pode conceder `BYPASSRLS`/acesso direto amplo às roles de runtime. Uso incorreto de `SET` com pool pode vazar tenant entre requisições.

**Remediação obrigatória:**

- manter `FORCE RLS`, mas criar roles executoras `NOLOGIN` distintas para lookup pré-auth e operações globais do worker; policies explícitas `TO` essas roles devem liberar somente as tabelas/operações necessárias;
- funções devem pertencer às roles executoras, usar `SECURITY DEFINER SET search_path = pg_catalog`, referenciar objetos como `wde.tabela`, não usar SQL dinâmico e validar tamanho/formato de todos os argumentos;
- revogar `PUBLIC EXECUTE` e conceder à role runtime apenas `EXECUTE`; ela não recebe `SET ROLE`, `BYPASSRLS` nem acesso direto equivalente às tabelas;
- autenticação deve usar função de lookup por prefixo limitado, retornar no máximo uma linha e preservar resposta externa/dummy verifier indistinguível; a comparação continua no Go com pepper fora do banco;
- após autenticar, iniciar transação explícita e executar `SELECT set_config('wde.workspace_id', $1, true)` parametrizado na mesma conexão; toda query tenant-scoped ocorre nessa transação, seguida obrigatoriamente de commit ou rollback antes de devolver a conexão ao pool;
- proibir `SET` de sessão, queries tenant-scoped em autocommit e interpolação do UUID; testar reutilização da mesma conexão após commit, rollback, panic e cancelamento;
- documentar que RLS reduz falhas acidentais, mas uma credencial PostgreSQL `wde_api` comprometida ainda pode escolher outro valor do GUC.

**Requisitos:** `SEC-001`, `SEC-002`, `SEC-003`, `SEC-018`, `SEC-019`.

### SCHEMA-SEC-002 — P0 — Replay conflita com a contagem e a unicidade das tentativas

Uma delivery em `dead_letter` normalmente já atingiu `max_attempts`. Alterá-la para `pending` sem definir o contador impede novo claim; zerar `attempt_count` colide com `UNIQUE (workspace_id, delivery_id, attempt_number)`. O modelo atual não distingue a execução original de replays.

**Impacto:** replay pode não executar, violar constraint ou reescrever a semântica do histórico append-only.

**Remediação obrigatória:** adicionar `run_number` monotônico à delivery, `attempts_in_run` e, em `delivery_attempts`, `run_number`, `attempt_number_in_run` e um `attempt_sequence` monotônico durante toda a vida. Replay válido incrementa `run_number`, zera apenas `attempts_in_run`, mantém `attempt_sequence` e `fencing_token`, e registra o novo `run_number` em `replay_commands`. Usar unique `(workspace_id, delivery_id, run_number, attempt_number_in_run)` e `(workspace_id, delivery_id, attempt_sequence)`. O comando de replay deve incluir `fingerprint_version`, comparar versão + fingerprint e realizar comando, auditoria e transição numa única transação.

**Requisitos:** `SEC-010`, `SEC-018`, `SEC-019`; `FR-015`, `FR-016`.

### SCHEMA-SEC-003 — P0 — Tombstone dentro do mesmo backup não impede ressurreição após restore

`workspace_tombstones` será revertida junto com o restante da base quando o snapshot for restaurado. Ela também não cobre API keys revogadas depois do snapshot em workspaces que continuam ativos.

**Impacto:** restore pode reativar workspace, endpoint ou credencial já revogados, contrariando `SEC-011`.

**Remediação obrigatória:** manter tráfego bloqueado após restore e adotar uma fonte de revogação externa ao snapshot, append-only e com geração monotônica, cobrindo workspace e API key. O reconciliador deve importar a geração mais nova e aplicar revogações antes da readiness. Se isso não existir no MVP, o procedimento seguro é revogar todas as API keys restauradas e exigir reemissão. O teste de restore precisa partir de backup anterior à revogação e provar que a chave antiga recebe `401` antes de liberar tráfego.

**Requisitos:** `SEC-003`, `SEC-011`, `SEC-016`, `SEC-025`.

### SCHEMA-SEC-004 — P0 — Purge e ações de FK não fecham o ciclo de vida referencial

Não foram definidos `ON DELETE`/`ON UPDATE`, ordem de purge nem o tratamento de payload vencido com deliveries ainda elegíveis ou em processamento. Também há conflito entre retenção curta de recursos e retenção maior de auditoria/replay que referencia esses recursos.

**Impacto:** loops de delivery sem payload, deleção bloqueada, cascata que apaga auditoria ou perda de atribuição histórica.

**Remediação obrigatória:** especificar todas as FKs e ações. Relações operacionais usam `ON DELETE RESTRICT` durante a vida ativa; purge administrativo remove em ordem explícita e em batches. Auditoria preserva IDs/snapshots mínimos sem cascade para entidades expurgáveis. Antes de remover payload, o job deve bloquear o evento, impedir novos replays e: aguardar deliveries ativas dentro de prazo limitado ou terminalizá-las atomicamente com categoria `payload_expired`; só então limpar ciphertext/nonce/versões. Workspace deve ser desativado e ter chaves/endpoints revogados antes do job, e o tombstone externo deve ser durável antes de apagar a linha ativa.

**Requisitos:** `SEC-010`, `SEC-011`, `SEC-018`, `SEC-019`.

### SCHEMA-SEC-005 — P1 — FKs compostas estão declaradas como intenção, não como contrato completo

Várias tabelas são descritas sem nomes/colunas exatos de PK, unique e FK. Faltam especialmente os vínculos compostos de secret/subscription/runtime para endpoint, delivery para evento+endpoint, attempt/replay para delivery e `actor_api_key_id` para a chave do mesmo tenant. `rate_limit_buckets` e `maintenance_jobs` permitem combinações opcionais sem `CHECK` por tipo.

**Impacto:** uma migration pode aceitar relações cross-tenant ou estados administrativos impossíveis mesmo que a aplicação filtre corretamente.

**Remediação obrigatória:** listar no DDL cada `UNIQUE (workspace_id, id)` e cada FK `(workspace_id, resource_id) REFERENCES ... (workspace_id, id)`. Definir FKs compostas para todos os vínculos acima; usar `MATCH FULL` quando o par puder ser nulo; adicionar `CHECK` que determine, por dimensão/tipo de job, quais IDs são obrigatórios, proibidos ou nulos. Chaves de ator que precisam sobreviver ao purge devem usar snapshot imutável do identificador em vez de cascade.

**Requisitos:** `SEC-001`, `SEC-016`, `SEC-018`, `SEC-019`.

### SCHEMA-SEC-006 — P1 — Envelope AEAD e fingerprints ainda permitem ambiguidades e oráculos offline

O schema guarda KEK e nonce, mas não materializa versão do formato/algoritmo do envelope. O AAD não é definido por tabela/campo; alterar `host_ascii`, `port`, `event_type` ou `key_id` pode não invalidar ciphertext relacionado. O fingerprint de evento como SHA-256 não chaveado permite testar payloads de baixa entropia contra um dump.

**Impacto:** transplante de ciphertext/metadado, incompatibilidade em rotação e inferência de payload apesar da criptografia.

**Remediação obrigatória:** para cada campo cifrado persistir `cipher_format_version`, `kek_version`, nonce e ciphertext, com `CHECK` all-or-none, nonce de 12 bytes, ciphertext com ao menos tag GCM e versões positivas/conhecidas. Especificar bytes canônicos de AAD por tabela, incluindo versão, `workspace_id`, tipo, ID e metadados de roteamento/semântica imutáveis relevantes (`scheme`, host canônico, porta, `event_type` ou `key_id`). Reencriptação sempre usa nonce novo. Trocar fingerprints persistidos derivados de payload por HMAC-SHA-256 com pepper próprio fora do banco e versão explícita, separada do pepper da chave de idempotência.

**Requisitos:** `SEC-007`, `SEC-008`, `SEC-009`, `SEC-010`.

### SCHEMA-SEC-007 — P1 — Recuperação de lease expirado não define o destino do attempt anterior nem o limite máximo

O claim inclui `processing` expirado, incrementa `attempt_count` e cria outro attempt, mas não exige `attempt_count < max_attempts` nem encerra atomicamente o attempt `started` anterior. No último attempt, um crash pode produzir `max_attempts + 1` ou deixar histórico indefinidamente aberto.

**Impacto:** violação de quota, histórico inconsistente e retries ilimitados em casos de crash repetido.

**Remediação obrigatória:** a operação privilegiada de claim deve, na mesma transação, bloquear a delivery, finalizar condicionalmente o attempt do fencing anterior como `abandoned`, decidir entre novo claim e `dead_letter`, incrementar token/contadores e inserir exatamente um novo `started`. Se o limite do run foi atingido, não cria nova chamada HTTP. Finalização deve atualizar delivery e somente o attempt `started` do mesmo `(delivery, run, fencing_token)`; zero linhas é stale e nunca altera estado já encerrado.

**Requisitos:** `SEC-016`, `SEC-019`; `FR-010`, `NFR-002`, `NFR-003`.

### SCHEMA-SEC-008 — P1 — “Append-only” ainda depende de grants genéricos

`delivery_attempts` precisa de uma atualização terminal única, enquanto `audit_events` não pode ser atualizado. O texto não fecha ownership, privilégios por coluna nem atomicidade entre ação sensível e auditoria.

**Impacto:** uma role de runtime pode receber `UPDATE` amplo por conveniência, adulterar histórico ou executar ação sem registro correspondente.

**Remediação obrigatória:** negar `INSERT/UPDATE/DELETE` direto nessas tabelas às roles runtime. Expor funções estreitas: uma para inserir/finalizar attempt com predicados de estado/fencing e outra para inserir auditoria allowlisted. A ação sensível e seu audit event devem confirmar ou reverter na mesma transação. Somente a função de attempt pode preencher colunas finais uma vez; nenhum caminho altera identidade, início ou outcome finalizado. Testes devem inspecionar ACLs, owners, `prosecdef`, `proconfig` e provar que SQL direto falha.

**Requisitos:** `SEC-018`, `SEC-019`, `SEC-020`.

### SCHEMA-SEC-009 — P1 — A ordem das migrations cria janela insegura e fixtures locais estão na cadeia produtiva

As tabelas surgem em `000002`–`000006`, mas RLS/revogações somente em `000007`. Objetos e funções novas também herdam privilégios default. Uma migration `000008_seed_local_only` na mesma sequência pode ser executada por engano em produção.

**Impacto:** deploy parcial expõe tabelas sem RLS/grants finais ou injeta dados/segredos sintéticos em ambiente real.

**Remediação obrigatória:** cada migration que cria tabela deve, na mesma transação, habilitar/forçar RLS, criar policy mínima e revogar acesso antes do commit. Configurar `ALTER DEFAULT PRIVILEGES` para owners efetivos, revogar `CREATE` em schemas acessíveis e `PUBLIC EXECUTE` em funções. O migrator deve `SET ROLE wde_owner` para ownership determinístico. Fixtures ficam fora da cadeia de produção, em comando/arquivo separado com dupla trava de ambiente e banco descartável. CI deve interromper uma migration em cada passo e provar que runtime continua fail-closed.

**Requisitos:** `SEC-001`, `SEC-002`, `SEC-022`, `SEC-023`.

### SCHEMA-SEC-010 — P1 — Índices e seleção de fila não demonstram isolamento contra DoS

Faltam índices normativos para fan-out por `(workspace_id, event_type)`, timelines de attempts, purge de payload/auditoria, expiração de rate limit e jobs agendados. O algoritmo também não define como descobre endpoints prontos nem como impede um workspace com backlog massivo de monopolizar o índice global `deliveries_ready_idx`.

**Impacto:** scans caros, timeout de purge e starvation de tenants, contrariando o isolamento exigido por `SEC-016`/`SEC-017`.

**Remediação obrigatória:** adicionar e validar com `EXPLAIN (ANALYZE, BUFFERS)` em massa representativa índices para: subscriptions `(workspace_id,event_type,endpoint_id)`; attempts `(workspace_id,delivery_id,attempt_sequence)`; eventos pendentes de purge `(payload_expires_at,id)` parcial; auditoria/metadata por expiração; buckets `(expires_at)`; jobs `(status,scheduled_at,id)`; e claim conforme o algoritmo definitivo. O scheduler deve limitar lote por workspace/endpoint e alternar candidatos de forma determinística; teste de saturação deve provar progresso de um tenant saudável enquanto outro mantém backlog máximo. Limites e `statement_timeout` precisam ser aplicados pela role, não apenas pelo handler.

**Requisitos:** `SEC-010`, `SEC-016`, `SEC-017`, `SEC-022`.

## 3. Controles aceitos

- UUIDv7 com CSPRNG possui aleatoriedade suficiente para IDs públicos do MVP, embora revele aproximadamente o instante de criação; IDs nunca são autorização. A migration deve validar `NOT NULL`, versão/variante quando praticável, e toda rota continua exigindo tenant autenticado.
- HMAC da `Idempotency-Key` com pepper separado, unique por workspace e transação `READ COMMITTED` são adequados; conflito deve comparar também `fingerprint_version` e nunca responder `202` antes de commit confirmado.
- `SKIP LOCKED`, relógio do PostgreSQL, lease e fencing são escolhas adequadas depois que `SCHEMA-SEC-007` fechar as transições.
- Separação entre roles de API, worker, admin, migrator e owner é adequada depois que ACLs, owners e funções forem materializados e testados.

## 4. Gate de saída

**Gate: REPROVADO.** Não iniciar implementação do schema nem considerar o passo 7 concluído enquanto `SCHEMA-SEC-001` a `SCHEMA-SEC-004` não forem resolvidos na arquitetura de dados e `SCHEMA-SEC-005` a `SCHEMA-SEC-010` não tiverem decisões normativas e testes associados.

Para aprovação, a próxima versão deve trazer:

1. matriz tabela × owner × grants × policy × função privilegiada;
2. DDL exato das chaves/FKs/checks, incluindo replay generation e envelope AEAD;
3. pseudocódigo transacional definitivo para auth context, claim/finalização, replay e purge;
4. protocolo de restore/revogação externo ao snapshot;
5. plano de índices com evidência de massa, fairness e timeouts;
6. plano de migrations fail-closed e fixtures fora da cadeia produtiva.

Depois dessas correções, o DDL real e as migrations ainda devem passar por uma revisão curta de implementação antes de qualquer release. Este parecer não certifica conformidade regulatória nem autoriza dados de terceiros.

## 5. Reavaliação da arquitetura de dados v1.1

**Data da reavaliação:** 2026-09-23  
**Artefatos reavaliados:** `webhook-delivery-engine-data-architecture.md` v1.1 e correção de fingerprint em `webhook-delivery-engine-architecture.md`  
**Relação com o parecer anterior:** esta seção não apaga os achados nem o gate inicial; registra as remediações que substituem aquele gate para fins de avanço do projeto.

### 5.1 Resultado por achado

| Achado | Status na v1.1 | Evidência e lacuna remanescente |
|---|---|---|
| `SCHEMA-SEC-001` | Remediado e materializado na Sprint 2 | Roles executoras `NOLOGIN`, policies específicas sob `FORCE RLS`, funções com `search_path = pg_catalog`, objetos qualificados e `PUBLIC EXECUTE` revogado. Testes PostgreSQL 17 inspecionam `prosecdef`, `proconfig`, owners, RLS, grants por coluna e ACL da sequence. |
| `SCHEMA-SEC-002` | Remediado | `run_number`, `attempts_in_run` e `attempt_sequence` separam retries de replays; uniques preservam histórico. Replay incrementa run, zera apenas o contador do run e mantém fencing/sequence monotônicos na mesma transação do comando e da auditoria. |
| `SCHEMA-SEC-003` | Remediado com estratégia conservadora | Restore entra em quarentena, revoga todas as chaves, suspende workspaces, desabilita endpoints e invalida leases antes da readiness. O runbook e o teste de restauração são obrigatórios; a proteção depende de a automação impedir promoção sem o comando one-shot. |
| `SCHEMA-SEC-004` | Remediado | FKs operacionais usam `RESTRICT`, a ordem de purge é explícita, auditoria não sofre cascade e payload só é removido depois de bloquear/reconciliar deliveries ativas e invalidar workers atrasados por fencing. |
| `SCHEMA-SEC-005` | Remediado | A seção 4.13 enumera FKs compostas e ações, snapshots de ator, `MATCH FULL` e checks por dimensão/tipo. O DDL continua sendo a prova final de completude. |
| `SCHEMA-SEC-006` | Remediado | Envelopes possuem versão de formato, KEK, nonce, tamanho mínimo e all-or-none; AAD canônico é definido por recurso e reencriptação exige nonce novo. Fingerprints de conteúdo agora usam HMAC-SHA-256 com pepper próprio tanto na arquitetura quanto no modelo de dados. |
| `SCHEMA-SEC-007` | Remediado no desenho | O fluxo fecha attempt expirado como `abandoned`, impede `max + 1`, decide `dead_letter` antes de nova chamada e cria claim+attempt atomicamente. A query ilustrativa anterior na seção 6 ainda não expressa sozinha todos esses ramos; a função/CTE definitiva deve seguir o algoritmo normativo em quatro passos e substituí-la nos testes/DDL. |
| `SCHEMA-SEC-008` | Remediado | Matriz de ownership e grants proíbe DML direto em attempts/auditoria; funções estreitas impõem transição terminal única e auditoria atômica com a ação sensível. |
| `SCHEMA-SEC-009` | Remediado | Default privileges e ownership são definidos no bootstrap; cada tabela nasce fail-closed com RLS/policy/revogação na mesma transação; fixtures saíram da cadeia produtiva e o CI testa interrupção entre migrations. |
| `SCHEMA-SEC-010` | Remediado na fatia implementada | Scheduler possui teto/cursor por workspace e endpoint, sequence sem lock global e deadline tipado por claim. Testes com locks independentes, saturação e `EXPLAIN (ANALYZE, BUFFERS)` sobre 5.000 jobs comprovam progresso e uso de `deliveries_ready_idx`; índices das fatias futuras continuam no backlog correspondente. |

### 5.2 Matriz materializada da fila na Sprint 2

| Objeto | Owner | Acesso da executora | Role login | RLS/policy |
|---|---|---|---|---|
| `workspaces.last_delivery_claim_sequence` | `wde_owner` | `SELECT` e `UPDATE` somente da coluna | nenhum DML direto | `FORCE RLS`; select/update somente workspace `active` |
| `endpoint_runtime.last_delivery_claim_sequence` | `wde_owner` | `SELECT`; update das colunas de lock/cursor/timestamp | nenhum DML direto | `FORCE RLS`; policy exclusiva da executora |
| `delivery_claim_sequence` | `wde_worker_executor` | uso por ownership dentro da função | sem `USAGE` para API/worker/admin/PUBLIC | não aplicável; sequence não possui RLS |
| `claim_deliveries` / `finalize_delivery` | `wde_worker_executor` | `SECURITY DEFINER`, `search_path=pg_catalog` | somente `wde_worker EXECUTE` | argumentos e transições validados |
| helpers `recover`, `select_candidate`, `claim_one` | `wde_worker_executor` | execução interna | sem `EXECUTE` para roles login/PUBLIC | funções individuais abaixo de 100 linhas |

`select_candidate` adquire apenas locks de workspace, endpoint runtime e delivery com `SKIP LOCKED`. A monotonicidade usa `nextval`, que não mantém row lock transacional global. O wrapper e os helpers continuam na mesma transação do statement, preservando claim, attempt e fencing atômicos. O deadline do contexto é aplicado também dentro do store PostgreSQL, de modo que cancelamento do driver não dependa do handler.

Durante os boundaries Goose 3–5, `claim_deliveries_v3_stage` e `finalize_delivery_v3_stage` não concedem `EXECUTE` a `PUBLIC` nem a roles login, enquanto os dois contratos v2 permanecem executáveis pelo worker. A `000006` troca nomes, grants e versão lógica na mesma transação; seu `down` restaura v2 e `schema_version=2` também atomicamente. O teste de boundary inspeciona existência e ACL em cada subida e descida e confirma que o runtime v3 falha fechado enquanto o banco anuncia v2.

### 5.3 Lacunas remanescentes não bloqueadoras para o backlog

As lacunas abaixo são evidências de implementação, não decisões arquiteturais ausentes:

1. materializar a matriz de acesso em DDL e comprovar roles reais, policies, owners, default privileges e funções `SECURITY DEFINER` em PostgreSQL 17;
2. manter os helpers privilegiados estreitos e o statement externo atômico ao evoluir replay, purge ou novas classes de job;
3. tornar o estado de quarentena de restore persistente e fail-closed, documentar o comando one-shot e provar que restart entre restore e reconciliação não libera readiness;
4. detalhar checks de coerência terminal no DDL, incluindo limpeza de `terminal_at` no replay, all-or-none dos envelopes e combinações permitidas de outcome/status;
5. repetir `EXPLAIN`, fairness e limites de contexto a cada mudança de índice/distribuição; purge concorrente pertence à sprint de retenção;
6. submeter as migrations e funções reais a revisão curta de segurança antes do primeiro release.

### 5.4 Gate final da reavaliação

**Gate: APROVADO CONDICIONALMENTE para backlog e implementação incremental.** Os quatro achados P0 e os seis P1 receberam decisões normativas suficientes na arquitetura de dados v1.1. Não resta bloqueador conceitual que justifique impedir o avanço ao backlog ou o início do primeiro corte vertical.

A aprovação não autoriza produção, exposição pública nem dados de terceiros. Ela fica condicionada a:

- cada task de banco carregar os testes e requisitos associados a `SCHEMA-SEC-001–010`;
- migrations permanecerem fail-closed durante toda a sequência, inclusive em falha parcial;
- testes de integração usarem as roles PostgreSQL reais, nunca apenas superuser;
- DDL, functions e grants implementados receberem novo review antes do release;
- qualquer simplificação de RLS, restore, AEAD, fencing, purge ou audit exigir ADR e nova revisão de segurança.

Com essas condições registradas, o passo 7 do fluxo `new-project` está concluído e o projeto pode avançar ao backlog.
