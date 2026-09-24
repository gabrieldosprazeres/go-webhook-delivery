# Security Review da Arquitetura: Webhook Delivery Engine

**Data:** 2026-09-23  
**Escopo:** arquitetura, ADR-001 a ADR-010 e `AGENTS.md`  
**Baseline:** `SEC-001` a `SEC-025`  
**Parecer final:** **APROVADO para avançar à arquitetura de dados**

## 1. Sumário executivo

A arquitetura atende os requisitos de segurança definidos na revisão do PRD e trata as principais fronteiras de confiança como invariantes do produto. Não foi encontrado bloqueador arquitetural pendente após as remediações incorporadas durante esta revisão.

O parecer não autoriza produção com dados de terceiros. Schema, migrations e papéis de banco ainda precisam de revisão específica; OpenAPI, implementação e infraestrutura deverão fornecer as evidências definidas neste documento.

## 2. Fronteiras avaliadas

```text
cliente não confiável
  → listener público / autenticação / autorização / quotas
  → casos de uso tenant-scoped
  → PostgreSQL com roles separadas
  → worker / keyrings em memória
  → resolver + cliente HTTP hostil
  → endpoint externo não confiável

listener operacional interno → métricas/probes mínimas
job administrativo one-shot → emissão/revogação de credenciais
Chaos Lab local → nunca presente na superfície produtiva
```

## 3. Resultado por área

| Área | Resultado | Evidência arquitetural |
|---|---|---|
| Tenant e autorização | Atendido | `Principal.WorkspaceID` deriva da API key; queries recebem ID + tenant; respostas cross-tenant são indistinguíveis |
| API keys | Atendido após remediação | 256 bits, verificador HMAC com pepper, comparação constante, revogação imediata e administração one-shot sem rota pública |
| HMAC e anti-replay | Atendido | protocolo `v1` canônico, timestamp, IDs, `key_id`, assinatura dupla na rotação e receptor de referência com deduplicação |
| SSRF | Atendido | canonicalização estrita, validação de todos A/AAAA, IP permitido fixado no `DialContext`, SNI original, proxy e redirects desabilitados |
| Segredos e payloads | Atendido | AES-256-GCM, AAD tenant/resource-scoped, keyrings fora do banco, plaintext de vida curta e consultas sem payload |
| Retenção/exclusão | Atendido em arquitetura | prazos definidos, purge idempotente, revogação antes da exclusão e reconciliação após restore |
| Fila e concorrência | Atendido | transações curtas, `SKIP LOCKED`, lease, fencing e atualização condicional com relógio do banco |
| Quotas e DoS | Atendido | limites globais, locais bounded e buckets persistentes; fan-out/paginação limitados |
| Observabilidade | Atendido | allowlist, cardinalidade limitada, listener operacional separado e canários de vazamento |
| Supply chain/container | Atendido | tools/actions pinadas, scanners, SBOM, imagem mínima não-root e CI com race/vulnerability gates |

## 4. Achados e remediações

### ARCH-SEC-001 — Bootstrap de credenciais estava implícito

- **Severidade original:** Média
- **Status:** Remediado
- **Risco:** a ausência de fluxo explícito poderia incentivar seed SQL, segredo versionado ou rota administrativa improvisada.
- **Remediação aplicada:** comandos one-shot no binário `api`, role administrativa separada, sem listener, auditoria obrigatória, arquivo `0600` em produção e restrição de bootstrap.

### ARCH-SEC-002 — Proteção dos secret files e TLS do PostgreSQL não estava normativa

- **Severidade original:** Média
- **Status:** Remediado
- **Risco:** leitura por outro usuário local, troca via symlink ou conexão TCP com proteção inferior à esperada.
- **Remediação aplicada:** owner/permission check, rejeição de symlink e exigência de verificação de cadeia/hostname para PostgreSQL TCP em produção.

### ARCH-SEC-003 — Limite de headers e preview externo estavam insuficientemente fechados

- **Severidade original:** Média
- **Status:** Remediado
- **Risco:** exaustão por headers e persistência acidental de segredo devolvido pelo destino.
- **Remediação aplicada:** `MaxResponseHeaderBytes` de 32 KiB e preview completamente desabilitado no MVP; habilitação futura requer review e criptografia.

### ARCH-SEC-004 — RLS permanece adiada para decisão do schema

- **Severidade:** Baixa, defesa em profundidade
- **Status:** Aceito condicionalmente para a próxima etapa
- **Risco residual:** bug de query ainda pode furar isolamento apesar dos filtros explícitos.
- **Exigência:** o Data Architect deve avaliar RLS por tabela/role, constraints compostas e impossibilidade de consulta pública sem tenant. Se RLS for descartada, a decisão deverá registrar testes e privilégios compensatórios.

### ARCH-SEC-005 — Gestão externa das KEKs não usa KMS no MVP

- **Severidade:** Baixa no escopo local/portfólio
- **Status:** Risco residual aceito
- **Condição:** dados sintéticos apenas. Hospedagem real exige secret manager/KMS, política de acesso, rotação operacional e novo review.

## 5. Invariantes obrigatórias para o schema

1. Toda tabela tenant-owned carrega `workspace_id`; FKs compostas impedem relações cross-tenant.
2. Idempotência é única por `(workspace_id, idempotency_key)` e inclui fingerprint versionado.
3. Claims e finalizações são operações SQL condicionais, nunca sequência read-then-write sem lock.
4. Fencing token é monotônico e não pode ser sobrescrito por valor fornecido pelo worker.
5. Attempts e audit events são append-only, com finalização monotônica estritamente limitada.
6. API, worker, migration e administração usam roles distintas com privilégios mínimos.
7. Ciphertext, nonce, versão da chave e campos necessários ao AAD possuem constraints consistentes.
8. Replay, quota e purge são idempotentes e possuem índices para limites de tempo previsíveis.
9. IDs públicos são opacos e não sequenciais; cursores são vinculados ao tenant.
10. Schema deve tornar impossível persistir estados e combinações temporais inválidas conhecidas.

## 6. Evidências exigidas durante implementação

- Matriz automatizada de todas as rotas usando recurso válido de outro workspace.
- Vetores HMAC independentes, alteração de cada campo, skew e rotação concorrente.
- Testes SSRF com IPv4/IPv6, mapped IPv6, IDN, DNS mutável, proxy, redirect e TLS inválido.
- Testes concorrentes de idempotência, claim, lease expirado e fencing obsoleto.
- Canários garantindo ausência de payload/segredo em logs, traces, métricas, erros e auditoria.
- Testes de limite para body, headers, resposta lenta/infinita, fan-out, paginação e quotas.
- Testes de permissões dos secret files, profiles e falha fechada no startup.
- Integração com roles PostgreSQL reais, não somente usuário superuser de teste.
- `go test -race`, Staticcheck, `govulncheck`, secret/image scan e SBOM no CI.

## 7. Riscos residuais aceitos

- `At-least-once` permite duplicação depois de falha ambígua; consumidores devem deduplicar por `delivery_id`.
- Administrador do banco/host pode contornar controles de aplicação; trilhas externas e mínimo privilégio continuam necessários.
- Rate limiting local de origem não é global; quotas autenticadas persistentes são a proteção distribuída do MVP.
- Listas de redes especiais mudam; dependências e testes precisam acompanhar novas faixas e metadata endpoints.
- Backups imutáveis expiram em até 35 dias; exclusão instantânea não é prometida.
- Sem KMS/HSM, o profile inicial é adequado a laboratório e dados sintéticos, não a compliance regulatória.

## 8. Gate

Arquitetura **aprovada** para o Passo 6 — Data Architect, sob as condições:

- incorporar os 10 invariantes de schema acima;
- submeter schema, roles, RLS e migrations a Security Review própria;
- não iniciar código de produção antes desses dois passos e do backlog rastreável;
- exigir novo ADR/review para mudar tenancy, HMAC, SSRF, retenção, lease/fencing ou semântica `at-least-once`.

