# User Stories: Webhook Delivery Engine

**Status:** Planejamento  
**Versão:** 1.0  
**Data:** 2026-09-23  
**Documento de origem:** `docs/webhook-delivery-engine-prd.md`

## 1. Convenções

- Prioridade `P0`: necessária para fechar a jornada principal do MVP.
- Prioridade `P1`: necessária para considerar a versão pronta para portfólio.
- Prioridade `P2`: evolução posterior ao MVP.
- Os critérios usam Given/When/Then e devem se tornar testes automatizados sempre que possível.
- Os IDs `FR-*`, `NFR-*` e `R-*` apontam para o PRD.
- “Aceito” significa persistido de forma durável; nunca significa entregue ao destino.

## 2. Mapa das histórias

| ID | História | Prioridade | Dependências | Rastreabilidade |
|---|---|---:|---|---|
| US-001 | Autenticar por API key e isolar workspace | P0 | — | FR-001; NFR-008 |
| US-002 | Cadastrar um endpoint seguro | P0 | US-001 | FR-002, FR-003, FR-019, FR-020; R-01 |
| US-003 | Gerenciar o segredo HMAC | P0 | US-002 | FR-004, FR-008; NFR-008 |
| US-004 | Publicar um evento idempotente | P0 | US-001, US-002 | FR-005, FR-006, FR-007; NFR-001, NFR-004, NFR-010 |
| US-005 | Entregar um webhook assinado | P0 | US-003, US-004 | FR-008, FR-009, FR-012, FR-020 |
| US-006 | Reservar trabalho com lease e fencing | P0 | US-004 | FR-010; NFR-002, NFR-003; R-03 |
| US-007 | Reprocessar falhas transitórias | P0 | US-005, US-006 | FR-012, FR-013; R-04 |
| US-008 | Isolar pressão por concorrência limitada | P0 | US-006, US-007 | FR-011; NFR-005, NFR-006 |
| US-009 | Encaminhar entregas esgotadas para DLQ | P0 | US-007 | FR-014 |
| US-010 | Consultar eventos, entregas e tentativas | P0 | US-005 | FR-016; NFR-008 |
| US-011 | Reenviar manualmente com auditoria | P0 | US-009, US-010 | FR-015 |
| US-012 | Encerrar e recuperar o serviço com segurança | P0 | US-006, US-008 | FR-017; NFR-001, NFR-002 |
| US-013 | Observar saúde, carga e falhas | P1 | US-004, US-005 | FR-018; NFR-007, NFR-013 |
| US-014 | Executar cenários reproduzíveis no Chaos Lab | P1 | US-005, US-007, US-013 | FR-022; NFR-014 |
| US-015 | Integrar e avaliar o projeto rapidamente | P1 | US-001–US-014 | FR-021; NFR-011, NFR-012, NFR-014 |

## 3. Épico A — Acesso e destinos

### US-001 — Autenticar por API key e isolar workspace

**Como** sistema produtor ou operador  
**Quero** acessar a API com uma credencial associada a um workspace e a escopos  
**Para** operar apenas os recursos que me pertencem e para os quais tenho permissão.

#### Critérios de aceitação

```gherkin
Cenário: chave válida com escopo suficiente
  Dado que existe uma API key ativa com o escopo exigido
  Quando uma requisição autenticada é enviada
  Então a operação é autorizada para o workspace da chave

Cenário: chave sem escopo suficiente
  Dado que a API key é válida mas não possui o escopo exigido
  Quando a operação é solicitada
  Então a API responde 403
  E nenhuma alteração é persistida

Cenário: isolamento entre workspaces
  Dado que um recurso pertence ao workspace A
  Quando uma chave do workspace B tenta consultá-lo ou alterá-lo
  Então a API não revela o recurso
  E nenhuma informação permite confirmar sua existência

Cenário: armazenamento seguro
  Dado que uma nova API key é emitida
  Quando sua criação é concluída
  Então o valor completo é exibido uma única vez
  E apenas seu prefixo identificador e hash não reversível são persistidos
```

### US-002 — Cadastrar um endpoint seguro

**Como** desenvolvedor integrador  
**Quero** cadastrar um destino e os tipos de evento de interesse  
**Para** receber somente os webhooks esperados sem permitir acesso a redes internas.

#### Critérios de aceitação

```gherkin
Cenário: cadastrar HTTPS público válido
  Dado um workspace autenticado com endpoints:write
  Quando cadastro uma URL HTTPS pública e tipos de evento válidos
  Então o endpoint é criado como ativo
  E recebe um identificador não sequencial

Cenário: bloquear destino proibido
  Dado uma URL que aponta ou resolve para loopback, rede privada, link-local, faixa reservada ou metadata
  Quando tento cadastrar o endpoint
  Então a API rejeita a operação com erro acionável

Cenário: restringir HTTP
  Dado que o serviço não está no modo local explicitamente habilitado
  Quando tento cadastrar uma URL HTTP
  Então a API rejeita a operação

Cenário: filtrar eventos
  Dado um endpoint inscrito somente no tipo invoice.paid
  Quando um evento customer.created é publicado
  Então nenhuma entrega é criada para esse endpoint
```

### US-003 — Gerenciar o segredo HMAC

**Como** desenvolvedor consumidor  
**Quero** criar e rotacionar um segredo de assinatura  
**Para** verificar a autenticidade dos webhooks sem interrupção abrupta.

#### Critérios de aceitação

```gherkin
Cenário: criação do segredo
  Dado um novo endpoint
  Quando seu segredo é gerado
  Então ele possui entropia adequada
  E é exibido somente nessa resposta
  E não aparece em logs, traces ou consultas posteriores

Cenário: rotação
  Dado um endpoint existente
  Quando o segredo é rotacionado
  Então novas tentativas usam o novo segredo
  E o segredo anterior permanece identificável somente durante a janela definida
  E a operação fica auditável sem registrar o valor secreto
```

## 4. Épico B — Ingestão confiável

### US-004 — Publicar um evento idempotente

**Como** sistema produtor  
**Quero** publicar um evento com uma chave de idempotência  
**Para** repetir uma requisição incerta sem criar eventos ou entregas duplicadas.

#### Critérios de aceitação

```gherkin
Cenário: primeira publicação
  Dado um evento válido e ao menos um endpoint elegível
  Quando envio POST /v1/events com Idempotency-Key
  Então evento e entregas são persistidos na mesma transação
  E a API responde 202 somente após o commit
  E retorna o identificador estável do evento

Cenário: repetição idêntica
  Dado um evento já aceito
  Quando repito a requisição com a mesma chave e o mesmo conteúdo lógico
  Então recebo o mesmo identificador de evento
  E nenhuma nova entrega é criada

Cenário: concorrência idempotente
  Dado cem requisições concorrentes com a mesma chave e o mesmo conteúdo
  Quando todas terminam
  Então existe exatamente um evento lógico
  E todas as respostas bem-sucedidas apontam para ele

Cenário: conflito da chave
  Dado um evento já aceito
  Quando reutilizo a chave com tipo, payload ou seleção de destinos diferente
  Então a API responde 409
  E o evento original permanece inalterado

Cenário: payload acima do limite
  Dado um corpo maior que 1 MiB no limite padrão
  Quando tento publicar o evento
  Então a API responde 413 antes da desserialização completa
  E nenhuma persistência parcial ocorre
```

## 5. Épico C — Entrega e concorrência

### US-005 — Entregar um webhook assinado

**Como** desenvolvedor consumidor  
**Quero** receber o corpo original, IDs estáveis e uma assinatura HMAC  
**Para** validar autenticidade e deduplicar reenvios.

#### Critérios de aceitação

```gherkin
Cenário: entrega bem-sucedida
  Dado uma entrega elegível e um destino saudável
  Quando o destino responde com qualquer status 2xx
  Então a entrega é marcada como concluída
  E uma tentativa append-only registra status, duração e instante

Cenário: assinatura válida
  Dado uma tentativa de entrega
  Quando o destino calcula HMAC-SHA256 usando timestamp e corpo bruto
  Então a assinatura recebida é verificável
  E evento e delivery possuem identificadores estáveis entre tentativas

Cenário: resposta excessiva
  Dado que o destino devolve um corpo maior que o limite configurado
  Quando a tentativa termina
  Então a leitura é limitada
  E somente uma versão truncada e sanitizada pode ser persistida
```

### US-006 — Reservar trabalho com lease e fencing

**Como** operador  
**Quero** executar múltiplos workers sobre a mesma fila persistente  
**Para** aumentar a capacidade sem permitir finalização por um worker obsoleto.

#### Critérios de aceitação

```gherkin
Cenário: claims concorrentes
  Dado várias entregas elegíveis e múltiplos workers
  Quando eles reservam trabalho simultaneamente
  Então cada versão de uma entrega é atribuída a no máximo um worker
  E as linhas bloqueadas não impedem progresso sobre outras entregas

Cenário: expiração do lease
  Dado um worker que morreu após reservar uma entrega
  Quando o lease expira
  Então a entrega volta a ficar elegível até TTL + 5 segundos

Cenário: fencing obsoleto
  Dado que um novo worker adquiriu a entrega após a expiração
  Quando o worker antigo tenta finalizar com o token anterior
  Então a atualização é recusada
  E o estado vigente não é alterado

Cenário: transação curta
  Dado uma entrega reservada
  Quando a chamada HTTP externa é feita
  Então não existe transação de banco aberta durante o I/O externo
```

### US-007 — Reprocessar falhas transitórias

**Como** operador  
**Quero** que falhas temporárias sejam reagendadas automaticamente  
**Para** atravessar indisponibilidades sem tempestade de requisições.

#### Critérios de aceitação

```gherkin
Cenário: erro transitório
  Dado timeout, falha de rede, 408, 425, 429 ou 5xx
  Quando a tentativa termina
  Então uma nova tentativa é agendada com backoff exponencial e jitter
  E o histórico anterior permanece imutável

Cenário: Retry-After permitido
  Dado uma resposta 429 com Retry-After dentro do limite
  Quando a próxima tentativa é calculada
  Então o valor informado é respeitado

Cenário: Retry-After abusivo
  Dado um Retry-After inválido ou acima do teto
  Quando a próxima tentativa é calculada
  Então a política limitada do serviço é aplicada

Cenário: falha permanente
  Dado uma resposta 4xx que não seja 408, 425 ou 429
  Quando a tentativa termina
  Então nenhum retry automático é agendado
```

### US-008 — Isolar pressão por concorrência limitada

**Como** operador  
**Quero** limitar concorrência global e por endpoint  
**Para** que um destino lento não consuma toda a capacidade nem crie goroutines sem limite.

#### Critérios de aceitação

```gherkin
Cenário: capacidade global cheia
  Dado que todos os slots do worker estão ocupados
  Quando o ciclo de claim roda
  Então nenhum trabalho além da capacidade livre é carregado em memória

Cenário: destino lento
  Dado um endpoint lento no limite de concorrência e outro saudável
  Quando há backlog para ambos
  Então o endpoint saudável continua progredindo
  E o lento não excede seu limite

Cenário: carga prolongada
  Dado um teste de carga com destinos bloqueados
  Quando o teste permanece ativo
  Então goroutines e memória permanecem limitadas pela configuração documentada
```

## 6. Épico D — Diagnóstico e recuperação

### US-009 — Encaminhar entregas esgotadas para DLQ

**Como** operador  
**Quero** identificar entregas que esgotaram a política  
**Para** investigar falhas definitivas sem manter retries infinitos.

#### Critérios de aceitação

```gherkin
Cenário: tentativas esgotadas
  Dado uma entrega no número máximo de tentativas
  Quando ocorre nova falha transitória
  Então a entrega muda para o estado de DLQ
  E categoria, motivo final e agenda ficam registrados
  E nenhuma tentativa automática adicional é criada
```

### US-010 — Consultar eventos, entregas e tentativas

**Como** operador  
**Quero** consultar a timeline de uma entrega  
**Para** explicar seu estado sem acesso direto ao banco.

#### Critérios de aceitação

```gherkin
Cenário: consultar timeline
  Dado uma entrega com várias tentativas
  Quando um cliente com deliveries:read consulta o recurso
  Então recebe estado atual e tentativas em ordem temporal
  E cada tentativa informa resultado, categoria, duração e status HTTP quando aplicável

Cenário: informação sensível
  Dado uma consulta autorizada
  Quando a resposta é montada
  Então ela não contém API key, segredo HMAC, assinatura interna ou payload confidencial não solicitado
```

### US-011 — Reenviar manualmente com auditoria

**Como** operador  
**Quero** solicitar novo envio de uma entrega falha  
**Para** recuperar um caso excepcional sem apagar sua história.

#### Critérios de aceitação

```gherkin
Cenário: replay autorizado
  Dado uma entrega permanente ou em DLQ
  Quando uma chave com deliveries:retry solicita replay com motivo
  Então a entrega volta a um estado processável conforme regra documentada
  E ator, instante e motivo são registrados
  E tentativas anteriores não são alteradas

Cenário: replay indevido
  Dado uma entrega já concluída ou uma chave sem permissão
  Quando o replay é solicitado
  Então a operação é rejeitada sem alterar o estado
```

### US-012 — Encerrar e recuperar o serviço com segurança

**Como** operador  
**Quero** que API e worker tratem sinais de encerramento  
**Para** implantar novas versões sem abandonar silenciosamente o trabalho.

#### Critérios de aceitação

```gherkin
Cenário: SIGTERM no worker
  Dado jobs em andamento
  Quando o worker recebe SIGTERM
  Então para de fazer novos claims
  E aguarda os jobs correntes até o prazo configurado
  E encerra em no máximo 30 segundos

Cenário: deadline de shutdown
  Dado uma tentativa que não termina dentro do prazo
  Quando o shutdown expira
  Então a execução é cancelada
  E o job permanece recuperável por expiração do lease

Cenário: reinício após crash
  Dado um evento aceito cujo worker morreu
  Quando o serviço volta e o lease expira
  Então a entrega é reprocessada sem perda do evento aceito
```

## 7. Épico E — Operabilidade e demonstração

### US-013 — Observar saúde, carga e falhas

**Como** operador  
**Quero** logs, métricas, traces e probes coerentes  
**Para** detectar indisponibilidade, backlog e degradação antes de acessar o banco.

#### Critérios de aceitação

```gherkin
Cenário: correlação
  Dado uma publicação e suas tentativas
  Quando observo logs e traces
  Então consigo correlacioná-los por IDs de evento e entrega
  E nenhum segredo ou payload completo é registrado

Cenário: cardinalidade de métricas
  Dado alto volume de workspaces, eventos e endpoints
  Quando métricas são exportadas
  Então IDs, URLs e chaves não são usados como labels

Cenário: PostgreSQL indisponível
  Dado que a dependência essencial não está utilizável
  Quando readiness é consultada
  Então ela indica indisponibilidade
  E liveness continua representando apenas a saúde do processo
```

### US-014 — Executar cenários reproduzíveis no Chaos Lab

**Como** avaliador técnico  
**Quero** acionar destinos locais com comportamentos determinísticos  
**Para** observar retries, rate limit, timeout, DLQ e assinatura sem serviço externo.

#### Critérios de aceitação

```gherkin
Cenário: falhar N vezes
  Dado um receptor configurado para falhar duas vezes e depois responder 200
  Quando um evento é publicado
  Então duas tentativas falham de modo observável
  E a terceira conclui a entrega

Cenário: cenários mínimos
  Dado o ambiente local
  Quando o Chaos Lab é executado
  Então oferece sucesso, timeout, 429 com Retry-After, falha temporária e falha permanente
  E pode verificar a assinatura recebida
```

### US-015 — Integrar e avaliar o projeto rapidamente

**Como** desenvolvedor ou avaliador técnico  
**Quero** executar o sistema, seus testes e benchmarks por comandos documentados  
**Para** validar as alegações do projeto sem depender do autor.

#### Critérios de aceitação

```gherkin
Cenário: quickstart
  Dado uma máquina com os pré-requisitos documentados
  Quando sigo o README
  Então publico e observo uma entrega local em até 10 minutos

Cenário: pipeline de qualidade
  Dado uma alteração no repositório
  Quando o CI é executado
  Então verifica formatação, testes, race detector, vet, análise estática e vulnerabilidades

Cenário: benchmark honesto
  Dado um relatório de performance
  Quando seus resultados são publicados
  Então hardware, configuração, massa, duração e limitações estão registrados
  E os resultados não são apresentados como garantia de produção
```

## 8. Definition of Done do MVP

O MVP está concluído somente quando:

- Todas as histórias P0 estão implementadas e aceitas.
- As histórias P1 de observabilidade, Chaos Lab e quickstart estão funcionais para a demonstração de portfólio.
- Os critérios críticos de idempotência, crash, lease, fencing, SSRF, retry e shutdown possuem testes automatizados.
- `go test -race ./...`, análise estática e verificação de vulnerabilidades passam no CI.
- O contrato OpenAPI, os ADRs, o threat model, o runbook e as limitações estão atualizados.
- As metas de performance foram medidas em ambiente descrito; metas não atingidas aparecem com análise honesta, não são ocultadas.
- Nenhuma alegação de `exactly-once` é feita.

## 9. Ordem sugerida para o backlog

1. Fundação do repositório, configuração e acesso: US-001.
2. Primeiro corte vertical: US-002, US-003, US-004 e US-005 com worker de concorrência 1.
3. Núcleo de confiabilidade: US-006, US-007 e US-012.
4. Backpressure e operação: US-008, US-009, US-010 e US-011.
5. Evidência de produção e portfólio: US-013, US-014 e US-015.

Esta ordem é apenas orientação de produto. A decomposição técnica e as sprints serão definidas depois das revisões de segurança, arquitetura e dados.

