# Design System e Developer Experience: Webhook Delivery Engine

**Status:** Aprovado para orientar arquitetura  
**Versão:** 1.0  
**Data:** 2026-09-23  
**Escopo vinculante do MVP:** API, OpenAPI, exemplos de terminal e Chaos Lab  
**Escopo pós-MVP:** console web descrita apenas como referência

## 1. Princípio de produto

O produto é API-first. A experiência principal deve permitir que um desenvolvedor complete a jornada por documentação e terminal, sem depender de dashboard:

```text
obter credencial → cadastrar endpoint → publicar evento → consultar delivery → entender tentativas → solicitar replay
```

A interface deve tornar explícita a diferença entre evento aceito e webhook entregue. Nenhuma mensagem pode sugerir `exactly-once`.

## 2. Linguagem e nomenclatura

Termos canônicos no contrato, documentação e telemetria:

| Conceito | Termo da API | Texto humano |
|---|---|---|
| Origem isolada | `workspace` | Workspace |
| Destino HTTP | `endpoint` | Endpoint |
| Entrada publicada | `event` | Evento |
| Envio para um destino | `delivery` | Entrega |
| Chamada HTTP individual | `attempt` | Tentativa |
| Fila de falhas esgotadas | `dead_letter` | DLQ |
| Reenvio operacional | `replay` | Reenvio manual |

Estados públicos de uma entrega:

- `pending`: aguardando processamento.
- `processing`: reservada por um worker.
- `retry_scheduled`: nova tentativa agendada.
- `succeeded`: destino respondeu com `2xx`.
- `failed_permanent`: erro classificado como não recuperável.
- `dead_letter`: política automática esgotada.
- `cancelled`: reservado para evolução; não usar no MVP sem ADR.

Nunca usar apenas “failed” quando o cliente precisa distinguir falha permanente de retry agendado.

## 3. Contrato de respostas

### 3.1 Envelope de sucesso

Recursos únicos são retornados diretamente, sem envelope genérico `data`. Coleções usam:

```json
{
  "items": [],
  "next_cursor": null
}
```

Datas usam RFC 3339 em UTC. IDs são strings opacas. Valores opcionais ausentes usam `null` somente quando a distinção entre desconhecido e vazio é relevante.

### 3.2 Erros

Erros usam `application/problem+json`:

```json
{
  "type": "https://docs.example.invalid/problems/idempotency-conflict",
  "title": "Idempotency key conflict",
  "status": 409,
  "code": "idempotency_conflict",
  "detail": "The key was already used with different content.",
  "request_id": "req_...",
  "errors": []
}
```

Regras:

- `code` é estável e adequado para automação; `detail` pode evoluir.
- Mensagens não revelam existência de recursos de outro workspace.
- Erros de validação apontam campos, nunca valores secretos completos.
- `request_id` é sempre retornado e correlaciona logs e traces.
- URLs do tipo de problema serão substituídas pelo domínio real somente quando existir documentação publicada.

## 4. Jornada principal por terminal

```text
$ wde endpoint create --url https://receiver.example/webhooks --events invoice.paid
Endpoint created: ep_01...
Signing secret: whsec_••••••••••••••••  (shown once; store it now)

$ wde event publish --type invoice.paid --idempotency-key demo-001 --data payload.json
Accepted: evt_01...
Delivery: dlv_01...  status=pending

$ wde delivery watch dlv_01...
ATTEMPT  RESULT       HTTP  DURATION  NEXT
1        retry        503   81ms      2s
2        retry        429   34ms      5s
3        succeeded    200   42ms      —
```

O binário `wde` é opcional e pós-núcleo. No MVP inicial, exemplos `curl` e respostas HTTP devem oferecer a mesma jornada. Não criar CLI antes de o contrato OpenAPI estabilizar.

## 5. Chaos Lab

O Chaos Lab é um executável local e uma API de teste, não uma rota administrativa exposta pelo serviço principal.

Modos canônicos:

- `success`: retorna `204` imediatamente.
- `fail_n_then_succeed`: retorna `503` N vezes e depois `204`.
- `rate_limit`: retorna `429` com `Retry-After` configurado.
- `timeout`: atrasa a resposta além do timeout do worker.
- `permanent_failure`: retorna `400`.
- `verify_signature`: valida timestamp, corpo bruto e HMAC.

A saída deve informar cenário, contador de chamadas e resultado da assinatura, sem imprimir segredo ou payload por padrão.

## 6. Mascaramento e conteúdo sensível

- API keys: mostrar apenas prefixo identificador, como `wde_live_a1b2…`.
- Segredo HMAC: mostrar integralmente apenas na criação/rotação; depois, somente `••••••••` e metadados.
- URL: consultas autenticadas podem mostrar scheme, host e path; logs usam host normalizado ou hash quando necessário, nunca query string.
- Payload: não aparece em logs, métricas ou traces. Na API, só é devolvido em operação explicitamente autorizada e definida posteriormente.
- Headers do destino: allowlist; nunca ecoar `Authorization`, cookies ou assinatura.
- Resposta externa: truncada, sanitizada e acompanhada por indicador `truncated`.

## 7. Acessibilidade da documentação

- Estado nunca é transmitido somente por cor; sempre exibir nome e ícone/texto.
- Exemplos funcionam sem realce de sintaxe.
- Tabelas possuem cabeçalhos claros e alternativa textual para diagramas.
- Mensagens evitam jargão sem definição; `at-least-once`, lease e fencing terão glossário.
- Comandos são copiáveis e não incluem segredos reais.
- O fluxo principal deve ser navegável por teclado caso uma console seja implementada.

## 8. Referência visual pós-MVP

Estes tokens existem apenas para evitar decisões visuais inconsistentes numa futura console. Eles não autorizam adicionar frontend ao MVP.

### 8.1 Tokens

| Token | Valor claro | Uso |
|---|---|---|
| `color.bg` | `#F8FAFC` | Fundo da aplicação |
| `color.surface` | `#FFFFFF` | Cartões e tabelas |
| `color.text` | `#0F172A` | Texto principal |
| `color.muted` | `#475569` | Texto secundário |
| `color.primary` | `#2563EB` | Ações e links |
| `color.success` | `#15803D` | Entrega concluída |
| `color.warning` | `#B45309` | Retry agendado |
| `color.danger` | `#B91C1C` | Falha permanente/DLQ |
| `color.processing` | `#6D28D9` | Processamento ativo |
| `focus.ring` | `#0EA5E9` | Foco visível |

Contraste mínimo WCAG AA. Tipografia de interface: `Inter, system-ui, sans-serif`; código e IDs: `JetBrains Mono, ui-monospace, monospace`. Escala: 12, 14, 16, 20, 24 e 32 px. Espaçamento baseado em 4 px. Bordas de 8 px e foco de 2 px.

### 8.2 Componentes de referência

- `StatusBadge`: ícone, nome completo e cor.
- `AttemptTimeline`: ordem temporal, resultado, duração, resposta e próxima tentativa.
- `SecretRevealOnce`: aviso, copiar e confirmação de armazenamento.
- `CodeExample`: linguagem, copiar e alternativa sem realce.
- `ProblemDetails`: título, código estável, detalhe e request ID.
- `MetricCard`: valor, unidade, período e estado de dados.
- `ConfirmReplayDialog`: delivery, impacto, motivo obrigatório e ator.

## 9. Wireframes pós-MVP

### 9.1 Lista de entregas

```text
┌───────────────────────────────────────────────────────────────────┐
│ Webhook Delivery Engine                         Workspace: demo    │
├───────────────────────────────────────────────────────────────────┤
│ Deliveries   Endpoints   Metrics                                  │
│ [status ▾] [endpoint ▾] [from — to] [Search by opaque ID]         │
├──────────────┬──────────────┬──────────────┬──────────┬────────────┤
│ Delivery     │ Event type   │ Endpoint     │ Status   │ Updated    │
│ dlv_01…      │ invoice.paid │ billing      │ RETRY    │ 2s ago     │
│ dlv_02…      │ user.created │ crm          │ SUCCESS  │ 8s ago     │
└──────────────┴──────────────┴──────────────┴──────────┴────────────┘
```

### 9.2 Detalhe e timeline

```text
┌───────────────────────────────────────────────────────────────────┐
│ ← Deliveries   dlv_01…   [RETRY SCHEDULED]        [Replay]        │
│ Event evt_01…  Endpoint ep_01…  Next attempt 14:32:05 UTC         │
├───────────────────────────────────────────────────────────────────┤
│ Attempts                                                          │
│ ● #1  503 transient_error  81 ms         14:31:58 UTC             │
│ ● #2  429 rate_limited    34 ms         14:32:00 UTC             │
│ ○ #3  scheduled                          14:32:05 UTC             │
├───────────────────────────────────────────────────────────────────┤
│ Request ID req_01…    Trace [open]    Response preview [redacted] │
└───────────────────────────────────────────────────────────────────┘
```

### 9.3 Navegação futura

```text
Deliveries → Delivery detail → Attempt detail
Endpoints  → Endpoint detail → Rotate secret
Metrics    → Queue health / outcomes / latency
```

Não haverá acesso por URL previsível sem autenticação e verificação de workspace. A UI nunca envia `workspace_id` como autoridade; a API deriva o tenant da credencial.

## 10. Gate de aceite para arquitetura

- O OpenAPI deve usar estados e erros deste documento.
- API, worker e telemetria devem usar o mesmo vocabulário.
- O Chaos Lab deve ser isolado da superfície pública.
- A arquitetura não deve incluir frontend no MVP.
- Qualquer CLI futura consumirá a API pública, sem acesso direto ao banco.
- Campos sensíveis devem ter política de exibição, log e retenção antes de serem implementados.

