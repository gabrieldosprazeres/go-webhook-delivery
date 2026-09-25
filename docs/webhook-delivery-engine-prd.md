# PRD: Webhook Delivery Engine

**Status:** Planejamento  
**Versão:** 1.2
**Data:** 2026-09-25
**Repositório:** `go-webhook-delivery`  
**Classificação:** Projeto novo — produto backend/API de alta confiabilidade  
**Próxima etapa obrigatória:** Security Review do PRD

## TL;DR

Serviço escrito em Go que permite a equipes de software registrar destinos, publicar eventos e entregar webhooks com semântica explícita de `at-least-once`, idempotência, retentativas seguras, rastreabilidade e proteção contra destinos maliciosos ou indisponíveis.

## 1. Contexto e problema

Entregar um webhook parece ser apenas executar uma requisição HTTP, mas uma implementação ingênua perde eventos em reinicializações, duplica entregas sem controle, sobrecarrega destinos instáveis, não explica falhas e pode abrir uma porta para SSRF. Equipes pequenas frequentemente constroem esse mecanismo dentro da própria aplicação, misturando regra de negócio com concorrência, persistência e tratamento de falhas.

O projeto resolve esse problema oferecendo uma API dedicada e um processo de entrega confiável. Para o portfólio, o produto também deve demonstrar domínio de Go em concorrência, cancelamento, I/O, confiabilidade distribuída, segurança, observabilidade e testes de falha — não apenas um CRUD.

### Evidência inicial

- O problema é recorrente em integrações entre sistemas: destinos podem responder lentamente, ficar indisponíveis ou processar a requisição e falhar antes de confirmar.
- A entrega para URLs controladas pelo usuário cria risco real de SSRF e vazamento de payloads e segredos.
- A hipótese de valor ainda não foi validada com usuários externos; no MVP, o projeto é tratado como produto de portfólio e laboratório técnico, e não como SaaS comercial validado.

## 2. Outcome desejado

Permitir que um desenvolvedor integre uma aplicação produtora a um mecanismo de webhooks confiável e observe o ciclo completo de um evento — aceitação, tentativas, sucesso ou DLQ — sem implementar infraestrutura de filas própria.

## 3. Objetivos

1. Aceitar eventos sem perdê-los depois de responder com sucesso ao produtor.
2. Entregar cada evento elegível uma ou mais vezes até sucesso ou esgotamento da política, deixando clara a semântica `at-least-once`.
3. Tornar duplicações controláveis por idempotência no ingresso e por identificadores estáveis na entrega.
4. Recuperar trabalho após crash, timeout ou encerramento, sem permitir que um worker obsoleto finalize uma tentativa mais nova.
5. Fornecer diagnóstico suficiente para entender cada tentativa sem expor segredos ou criar cardinalidade ilimitada.
6. Demonstrar um projeto Go de nível pleno/sênior reproduzível localmente, testado e bem documentado.

## 4. Não objetivos

- Substituir Kafka, Redis Streams ou um broker genérico de mensagens.
- Oferecer semântica `exactly-once`; destinos também devem tratar duplicações.
- Processar eventos arbitrários entre consumidores internos ou oferecer pub/sub genérico.
- Oferecer signup, gestão de equipes, billing ou Grafana público no primeiro release do console.
- Oferecer billing, planos comerciais, organizações complexas ou marketplace de integrações.
- Usar Kafka, Redis ou Kubernetes no MVP.
- Suportar plugins, transformações de payload ou execução de código fornecido pelo usuário.
- Entregar webhooks recebidos de provedores específicos; o MVP recebe eventos pela sua própria API.

## 5. Personas e Jobs-to-be-Done

### Persona 1 — Desenvolvedor integrador

- **Perfil:** desenvolve uma aplicação que precisa avisar sistemas externos quando eventos de negócio acontecem.
- **Job:** Quando minha aplicação produzir um evento importante, quero delegar a entrega do webhook com uma chamada simples, para que eu não precise construir retentativas, concorrência e rastreamento de falhas.
- **Dores atuais:** código acoplado à aplicação, perda de eventos em crashes, retentativas improvisadas, baixa visibilidade e receio de duplicações.
- **Ganhos esperados:** contrato claro, resposta rápida após persistência, idempotência e consulta do estado da entrega.

### Persona 2 — Operador da plataforma

- **Perfil:** pessoa responsável por operar o serviço e responder a incidentes.
- **Job:** Quando entregas começarem a falhar ou o serviço reiniciar, quero identificar a causa, acompanhar backlog e recuperar trabalho com segurança, para restaurar o fluxo sem corromper estados nem reenviar tudo indiscriminadamente.
- **Dores atuais:** logs insuficientes, falhas silenciosas, filas travadas e reprocessamento manual inseguro.
- **Ganhos esperados:** métricas, traces, histórico de tentativas, DLQ, replay auditável e shutdown previsível.

### Persona 3 — Avaliador técnico

- **Perfil:** tech lead, dev sênior ou recrutador técnico avaliando o projeto.
- **Job:** Quando eu revisar ou executar o repositório, quero verificar decisões e propriedades de confiabilidade por testes reproduzíveis, para distinguir domínio real de Go de uma aplicação CRUD gerada automaticamente.
- **Dores atuais:** projetos de portfólio sem falhas simuladas, métricas, trade-offs ou evidência de qualidade.
- **Ganhos esperados:** quickstart curto, documentação honesta, testes de concorrência/falha e resultados mensuráveis.

## 6. Controle de acesso

**Abordagem escolhida para o MVP:** A — permissões fixas expressas por escopos de API key.

O primeiro release do console aceita uma API key existente e cria uma sessão humana curta, server-side. A API pública permanece Bearer-only. Não haverá cadastro de usuário, senha, SSO ou emissão de credencial pela UI. Cada workspace mantém credenciais de alta entropia, armazenadas de modo não reversível e associadas a escopos fixos. A separação por workspace é obrigatória.

| Escopo | Permissões de alto nível |
|---|---|
| `events:write` | Publicar eventos |
| `endpoints:write` | Criar, alterar, desabilitar e rotacionar segredo de destinos |
| `deliveries:read` | Consultar eventos, entregas e tentativas |
| `deliveries:retry` | Solicitar replay manual auditável |
| `admin` | Todas as permissões do workspace |

**Fora do primeiro release:** contas humanas persistentes, JWT, roles configuráveis e interface administrativa. O console é uma experiência derivada da API key, não um novo sistema de identidade.

## 7. Integrações externas

| Integração | Propósito | Sandbox | Custo/limite | Observações |
|---|---|---|---|---|
| Endpoint HTTP/HTTPS cadastrado pelo usuário | Receber webhooks enviados pelo serviço | O projeto fornecerá um receptor local de teste | Definido pelo destino | Integração arbitrária e não confiável; exige timeouts, limites, HMAC e defesa contra SSRF |
| OpenTelemetry/Prometheus compatível | Exportar telemetria | Local no ambiente de desenvolvimento | Sem dependência comercial obrigatória | O MVP deve funcionar sem fornecedor proprietário |

O produto não depende de API comercial para completar sua jornada principal.

## 8. Opportunity Solution Tree

```text
Outcome: evento aceito percorre um fluxo confiável, seguro e observável até sucesso ou DLQ
├── Oportunidade: evitar perda e duplicação descontrolada
│   ├── Entrega síncrona durante o request
│   ├── Broker externo dedicado
│   └── Persistência transacional + worker com leases/fencing ← escolhida
├── Oportunidade: recuperar destinos instáveis sem tempestade de tráfego
│   ├── Retry imediato em loop
│   ├── Retry manual
│   └── Backoff exponencial com jitter + DLQ ← escolhida
├── Oportunidade: permitir verificação da origem pelo destino
│   ├── API key no payload
│   ├── Assinatura HMAC com timestamp ← escolhida
│   └── mTLS obrigatório
└── Oportunidade: diagnosticar falhas e provar comportamento
    ├── Logs livres
    ├── Estado atual apenas
    └── Histórico de tentativas + métricas + traces + testes de falha ← escolhida
```

As soluções escolhidas completam a jornada com o menor número de componentes operacionais para o MVP.

## 9. Hipóteses

| ID | Hipótese | Evidência de validação | Risco |
|---|---|---|---|
| H-01 | Desenvolvedores valorizam delegar entrega e retries a um serviço especializado | Uma integração de referência publica e acompanha um evento sem implementar retry próprio | Médio |
| H-02 | PostgreSQL é suficiente para a escala-alvo inicial sem Kafka/Redis | Metas de ingestão e entrega são atingidas no benchmark documentado sem perda em testes de crash | Médio |
| H-03 | `at-least-once` é aceitável quando idempotência e IDs estáveis são explícitos | Documentação e receptor de teste demonstram deduplicação e nenhuma alegação de `exactly-once` | Baixo |
| H-04 | Um avaliador técnico consegue reconhecer profundidade pela evidência, não pela quantidade de componentes | Quickstart, ADRs futuros, testes de corrida/falha e relatório de benchmark são reproduzíveis | Médio |
| H-05 | Proteções de SSRF podem bloquear destinos perigosos sem impedir HTTPS público legítimo | Suíte de segurança bloqueia faixas proibidas e permite destinos públicos válidos | Alto |

## 10. Validação dos quatro riscos

| Risco | Avaliação | Mitigação no planejamento |
|---|---|---|
| Valor | Médio: valor técnico é claro, valor comercial ainda não foi validado | Tratar como produto de portfólio; medir tempo de integração e clareza do diagnóstico; coletar feedback de revisores antes de expandir |
| Usabilidade | Médio: produto API-first pode ser difícil sem boa documentação | OpenAPI, exemplos copiáveis, erros acionáveis, receptor local e quickstart de até 10 minutos |
| Viabilidade | Médio: leases, fencing e SSRF exigem rigor | MVP sem broker/Kubernetes/dashboard; histórias pequenas; testes de integração, concorrência e falha como critérios de aceite |
| Negócio | Baixo no objetivo atual: sem venda nem dados regulados por definição | Não prometer compliance; tratar payloads como potencialmente sensíveis; definir retenção e termos antes de qualquer oferta pública |

O risco H-05 deverá ser revisto na próxima etapa obrigatória de Security Review antes da arquitetura.

## 11. Jornada e Story Map

```text
Preparar acesso → Cadastrar destino → Publicar evento → Entregar com segurança → Acompanhar → Recuperar falhas → Operar

MVP:
API key          endpoint + HMAC    idempotência        worker + lease/fencing    status/tentativas    retries/DLQ/replay    métricas/shutdown

v1.1:
gestão humana    rotação avançada   lote                circuit breaker           filtros avançados    replay em lote        alertas prontos

v2:
SSO/RBAC         templates          ingestão externa    regiões/HA                 dashboard            políticas custom      autoscaling/K8s
```

O corte MVP completa a jornada de ponta a ponta por API e ferramentas locais. Dashboard não é necessário para completar o job.

## 12. Escopo do MVP

| ID | Capacidade | MoSCoW | Justificativa |
|---|---|---|---|
| FR-001 | Autenticar API keys por workspace e escopo | Must | Protege e isola todas as operações |
| FR-002 | Criar, consultar, atualizar e desabilitar endpoints HTTPS | Must | Sem destino não há jornada de entrega |
| FR-003 | Configurar filtro por tipo de evento | Must | Impede entregas irrelevantes |
| FR-004 | Gerar e rotacionar segredo usado para HMAC sem expô-lo posteriormente | Must | Permite verificar autenticidade e trocar credencial |
| FR-005 | Aceitar evento com `Idempotency-Key`, tipo e payload | Must | Entrada principal do produto |
| FR-006 | Persistir evento e entregas elegíveis antes de responder `202 Accepted` | Must | Evita reconhecer trabalho ainda não durável |
| FR-007 | Detectar repetição idêntica da chave e rejeitar conflito de conteúdo | Must | Torna retries do produtor previsíveis |
| FR-008 | Entregar via HTTP com HMAC-SHA256 sobre timestamp e corpo bruto | Must | Saída principal com autenticidade verificável |
| FR-009 | Garantir semântica `at-least-once` e ID estável de evento/entrega | Must | Contrato central de confiabilidade |
| FR-010 | Reservar trabalho com lease e impedir finalização por worker obsoleto por fencing | Must | Evita corrupção em concorrência e recuperação |
| FR-011 | Aplicar concorrência global e por endpoint com backpressure | Must | Protege serviço e destinos |
| FR-012 | Classificar respostas e falhas em sucesso, retry ou falha permanente | Must | Define comportamento verificável |
| FR-013 | Reagendar falhas transitórias com backoff exponencial e jitter, respeitando `Retry-After` dentro de limite | Must | Recupera falhas sem sincronizar tempestades |
| FR-014 | Mover entrega esgotada para DLQ lógica | Must | Encerra políticas finitas sem perder diagnóstico |
| FR-015 | Permitir replay manual de entrega falha, com registro auditável | Must | Completa fluxo de recuperação |
| FR-016 | Expor consulta de evento, entrega e histórico append-only de tentativas | Must | Diagnóstico é parte do job principal |
| FR-017 | Encerrar graciosamente e recuperar leases abandonados | Must | Comportamento de produção e portfólio |
| FR-018 | Exportar logs estruturados, métricas e traces correlacionáveis | Must | Operabilidade e evidência técnica |
| FR-019 | Validar destinos e conexões contra SSRF, incluindo redirects e mudança de resolução | Must | Serviço realiza chamadas para URLs fornecidas pelo usuário |
| FR-020 | Limitar payload, tempo de conexão/resposta, redirects e corpo de resposta | Must | Evita exaustão e abuso |
| FR-021 | Publicar contrato OpenAPI, quickstart e exemplo de verificação HMAC | Should | Reduz tempo de adoção e permite avaliação |
| FR-022 | Fornecer receptor local de teste com modos de sucesso, timeout, 4xx e 5xx | Should | Torna comportamento reproduzível |

## 13. Regras de negócio do MVP

1. Uma resposta de ingestão `202 Accepted` só pode ocorrer após a persistência durável do evento e de todas as entregas elegíveis na mesma operação lógica.
2. A unicidade de idempotência é por workspace e `Idempotency-Key`.
3. Repetir a chave com o mesmo conteúdo lógico retorna o mesmo evento e não cria novas entregas; repetir com conteúdo diferente retorna conflito.
4. Respostas `2xx` são sucesso. Falhas de rede, timeout, `408`, `425`, `429` e `5xx` são transitórias. Outros `4xx` são permanentes por padrão.
5. `Retry-After` só é aceito dentro de um limite configurado; fora dele aplica-se a política padrão.
6. O serviço não garante entrega única. O mesmo `delivery_id` acompanha todas as tentativas para permitir deduplicação no consumidor.
7. Uma entrega só pode ser finalizada pelo detentor do lease e fencing token vigentes.
8. Nenhuma chamada HTTP externa permanece dentro da transação usada para reservar trabalho.
9. O segredo HMAC é mostrado apenas na criação/rotação; logs e respostas posteriores não o revelam.
10. Replay manual cria uma nova ação auditável sem apagar ou alterar o histórico anterior.

## 14. Critérios de aceite do MVP

- [ ] Um evento aceito permanece recuperável após encerramento abrupto do processo.
- [ ] Cem requisições concorrentes com a mesma chave e conteúdo resultam em um único evento lógico e no mesmo identificador retornado.
- [ ] A mesma chave com conteúdo diferente retorna `409 Conflict` e não altera o evento existente.
- [ ] Dois workers não conseguem finalizar validamente a mesma tentativa com fencing tokens diferentes.
- [ ] Falhas transitórias seguem uma agenda de retry com jitter dentro das faixas documentadas; falhas permanentes não entram em retry automático.
- [ ] Após esgotar a política, a entrega fica em DLQ e pode ser reenviada manualmente sem apagar tentativas anteriores.
- [ ] O destino consegue validar a assinatura HMAC usando timestamp e corpo bruto e rejeitar mensagem adulterada.
- [ ] URLs locais, privadas, link-local, reservadas e de metadata são bloqueadas, inclusive após resolução e redirect.
- [ ] O processo atende a `SIGTERM`, para de reservar novo trabalho e encerra em até 30 segundos, preservando ou liberando trabalho recuperável.
- [ ] Cada tentativa é correlacionável por IDs em estado, logs e trace, sem segredos nem payload completo nos logs.
- [ ] O quickstart permite publicar e observar uma entrega local de sucesso em até 10 minutos a partir de uma máquina com os pré-requisitos documentados.

## 15. Requisitos não funcionais

| ID | Categoria | Requisito testável |
|---|---|---|
| NFR-001 | Confiabilidade | Zero eventos confirmados como aceitos perdidos na suíte automatizada de crashes e reinícios |
| NFR-002 | Recuperação | Trabalho de worker interrompido volta a ficar elegível até `TTL do lease + 5s` |
| NFR-003 | Consistência | Zero finalizações aceitas com fencing token obsoleto nos testes concorrentes |
| NFR-004 | Performance | Em cenário local de referência documentado, sustentar ao menos 100 eventos/s de ingestão com p95 inferior a 200 ms |
| NFR-005 | Vazão | Em cenário local com receptor saudável, sustentar ao menos 100 entregas/s, sem perda de evento aceito |
| NFR-006 | Backpressure | Uso de goroutines e memória permanece limitado pelos valores configurados quando o destino não responde |
| NFR-007 | Disponibilidade operacional | Readiness fica negativa enquanto dependências essenciais não permitem aceitar trabalho com segurança; liveness não depende de destino externo |
| NFR-008 | Segurança | Nenhum segredo, API key completa ou payload completo aparece em logs estruturados, traces ou mensagens de erro |
| NFR-009 | Segurança | Validação SSRF ocorre no cadastro e no momento da conexão; redirects seguem a mesma política ou são bloqueados |
| NFR-010 | Limites | Payload máximo padrão de 1 MiB; excesso retorna `413` sem persistência parcial |
| NFR-011 | Qualidade | Suíte passa com detector de corrida; testes críticos de idempotência, leases, fencing, shutdown e SSRF são automatizados |
| NFR-012 | Segurança de dependências | Pipeline não contém vulnerabilidade conhecida High/Critical sem exceção documentada e aprovada |
| NFR-013 | Observabilidade | Métricas evitam labels com IDs de workspace, evento, entrega, endpoint ou URL; correlação detalhada ocorre em logs/traces |
| NFR-014 | Portabilidade | Jornada principal executa localmente com Go, PostgreSQL e contêineres, sem Kafka, Redis, Kubernetes ou serviço pago |
| NFR-015 | Manutenibilidade | Cancelamento e deadlines são propagados; nenhum processamento crítico depende de goroutine órfã/fire-and-forget |

As metas de performance são objetivos de laboratório, não promessa comercial. O relatório deverá registrar hardware, massa de dados, configuração e limitações.

## 16. Métricas de sucesso

| Métrica | Baseline | Meta do MVP | Prazo de avaliação |
|---|---|---|---|
| Eventos aceitos perdidos em testes de crash | Não medido | 0 | Antes do release v1.0 |
| Duplicação lógica no ingresso idempotente | Não medido | 0 em 100 requests concorrentes iguais | Antes do release v1.0 |
| Finalizações com fencing obsoleto | Não medido | 0 na suíte concorrente | Antes do release v1.0 |
| Tempo de recuperação após worker morto | Não medido | Até TTL do lease + 5s | Antes do release v1.0 |
| Ingestão local | Não medido | ≥100 eventos/s e p95 <200 ms | Antes do release v1.0 |
| Entrega local para receptor saudável | Não medido | ≥100 entregas/s | Antes do release v1.0 |
| Tempo de quickstart | Não medido | ≤10 minutos | Primeiro teste com terceiro |
| Corridas detectadas | Não medido | 0 | Em cada pipeline de CI |
| Vulnerabilidades High/Critical sem exceção | Não medido | 0 | Em cada pipeline de CI |

## 17. Fora do MVP

| Capacidade | Motivo | Quando revisar |
|---|---|---|
| Dashboard web | API, OpenAPI e observabilidade completam a jornada; UI ampliaria stack e desviaria do aprendizado de Go | Após a confiabilidade do v1 estar comprovada |
| Kafka/Redis | PostgreSQL atende a hipótese de escala inicial e mantém operação simples | Se benchmarks evidenciarem gargalo real |
| Kubernetes/autoscaling | Não necessário para provar propriedades do serviço | Depois de uma estratégia de deploy simples e carga real |
| Entrega regional/alta disponibilidade multi-região | Complexidade operacional incompatível com MVP de portfólio | Se houver uso real com requisitos de continuidade |
| Transformação e templates de payload | Aumenta superfície de execução e segurança | Após feedback de integradores |
| Retry/replay em lote | Pode ser operado individualmente no MVP | v1.1, se o volume tornar o fluxo manual inviável |
| Circuit breaker avançado | Backoff, limites e backpressure cobrem o primeiro ciclo | v1.1 após métricas reais |
| Usuários, login, SSO e RBAC dinâmico | Não há UI humana no MVP | Junto de eventual dashboard/SaaS |
| Billing e planos | Não existe hipótese comercial validada | Somente após discovery comercial |
| Garantia `exactly-once` | Impossível garantir ponta a ponta apenas pelo emissor | Não planejar; educar consumidores sobre idempotência |

## 18. Riscos e mitigação

| ID | Risco | Probabilidade | Impacto | Mitigação |
|---|---|---|---|---|
| R-01 | SSRF por URL, DNS rebinding ou redirect | Alta | Alto | Política de rede em camadas, validação no connect e testes adversariais; Security Review obrigatório |
| R-02 | Evento confirmado sem entrega persistida | Média | Alto | Aceite somente após persistência atômica e teste de crash |
| R-03 | Corrida entre lease expirado e tentativa ainda em andamento | Média | Alto | Fencing obrigatório e testes concorrentes determinísticos |
| R-04 | Retry storm contra destino degradado | Média | Alto | Backoff com jitter, `Retry-After` limitado, concorrência por endpoint e backpressure |
| R-05 | Vazamento de dados em logs/telemetria | Média | Alto | Redação por padrão, ausência de payload completo e testes de não vazamento |
| R-06 | Scope creep para broker/SaaS/dashboard | Alta | Médio | Não objetivos e fora do MVP explícitos; revisar somente após métricas do v1 |
| R-07 | Complexidade gerada por IA sem domínio comprovável | Alta | Médio | Alterações pequenas, decisões documentadas, testes de falha, revisão humana e capacidade de explicar cada invariável |
| R-08 | Benchmark enganoso | Média | Médio | Ambiente, carga, configuração e metodologia documentados; não extrapolar resultado local |
| R-09 | Payload contendo dados pessoais/regulados | Média | Alto | Tratar como sensível, minimizar retenção e logs; definir política formal antes de exposição pública |

## 19. Suposições e restrições

### Suposições

- O produtor consegue usar uma API HTTP e fornecer uma chave de idempotência.
- O consumidor aceita a semântica `at-least-once` e pode deduplicar pelo identificador estável.
- Os endpoints do MVP usam HTTPS; HTTP é permitido apenas no ambiente local de teste explicitamente configurado.
- PostgreSQL é a única dependência persistente necessária para atingir as metas iniciais.

### Restrições

- Implementação principal em Go.
- API, worker e PostgreSQL compõem o produto mínimo.
- Sem Kafka, Redis e Kubernetes no MVP.
- Sem dashboard no MVP.
- O projeto não declara conformidade LGPD, PCI, HIPAA ou SOC 2; antes de hospedar payloads reais de terceiros, retenção, exclusão, residência e resposta a incidentes precisarão de definição formal.

## 20. Priorização do MVP

O critério principal não é quantidade de features, mas fechamento da jornada com propriedades demonstráveis. Em ordem:

1. Durabilidade e idempotência no ingresso.
2. Entrega assinada para um destino seguro.
3. Leases, fencing, retries e DLQ.
4. Consulta, replay e histórico.
5. Shutdown, observabilidade, segurança e evidência automatizada.

Itens de UI, brokers e orquestração não competem por prioridade nesta versão, pois não removem risco central nem são necessários ao outcome.

## 21. Rastreabilidade de produto

Os requisitos `FR-*`, `NFR-*`, hipóteses `H-*` e riscos `R-*` são ligados às user stories em `docs/webhook-delivery-engine-user-stories.md`. Arquitetura, schema e backlog serão definidos nas etapas posteriores; este documento define comportamento e resultados, não componentes internos detalhados.

## 22. Histórico de versões

| Versão | Data | Mudança |
|---|---|---|
| 1.0 | 2026-09-23 | PRD inicial do projeto novo |
