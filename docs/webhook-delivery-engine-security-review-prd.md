# Security Review do PRD: Webhook Delivery Engine

**Status:** Aprovado condicionalmente — requisitos bloqueadores devem orientar arquitetura, schema e backlog  
**Versão:** 1.0  
**Data:** 2026-09-23  
**Escopo revisado:** `webhook-delivery-engine-prd.md` e `webhook-delivery-engine-user-stories.md`  
**Etapa do planejamento:** Security Review do PRD (passo 2)

## 1. Parecer executivo

O PRD já trata corretamente vários riscos incomuns em projetos de portfólio: separação por workspace, escopos de API key, semântica `at-least-once`, HMAC, limites de payload, logs sem conteúdo integral, SSRF incluindo redirects e resolução, análise de dependências e operações auditáveis. A direção é adequada.

A aprovação é condicional porque alguns controles ainda estão descritos apenas como intenção. Antes de escolher componentes ou fechar o schema, a arquitetura deve transformar em invariantes verificáveis:

1. toda autorização e consulta deve ser limitada pelo `workspace_id` derivado da credencial, nunca por um workspace informado pelo cliente;
2. o protocolo de assinatura e a rotação HMAC devem ser determinísticos e resistentes a replay;
3. a conexão de saída deve usar somente endereços já resolvidos e validados, sem nova resolução implícita pelo transporte;
4. segredos, payloads e fragmentos de resposta precisam de criptografia, retenção e exclusão definidas;
5. ingestão, fan-out, consultas e replay precisam de limites que evitem DoS e amplificação;
6. endpoints operacionais e modos de diagnóstico não podem ampliar a superfície pública.

Esses pontos não exigem Kafka, Redis, Kubernetes, login humano ou um sistema de IAM complexo. São propriedades do mesmo monólito modular proposto no MVP.

## 2. Modelo de ameaça resumido

### Ativos

- API keys, seus metadados e escopos;
- segredos HMAC atuais e anteriores;
- payloads dos eventos e fragmentos de resposta dos destinos;
- separação entre workspaces;
- integridade do histórico de tentativas e ações de replay;
- disponibilidade da API, worker, banco e destinos externos;
- confiabilidade das métricas, logs, traces e registros de auditoria.

### Atores e fronteiras de confiança

| Ator/fronteira | Confiança | Risco principal |
|---|---|---|
| Cliente autenticado da API | Parcial | abuso de escopo, enumeração, exaustão e acesso cruzado |
| Payload fornecido pelo produtor | Não confiável | vazamento, volume, conteúdo malformado e log injection |
| URL, DNS e servidor de destino | Hostis por padrão | SSRF, rebinding, redirect, slowloris e respostas excessivas |
| Rede entre componentes | Não confiável fora do ambiente local | interceptação, downgrade e acesso a endpoints operacionais |
| PostgreSQL | Componente privilegiado | exposição em dump, adulteração por credencial comprometida e indisponibilidade |
| Pipeline, imagem e dependências | Não confiáveis até verificação | dependência vulnerável, artefato adulterado e segredo em build |
| Operador | Privilegiado, mas auditável | replay indevido, leitura de dados sensíveis e alteração de configuração |

### Objetivos de segurança

- impedir acesso entre tenants mesmo quando IDs válidos forem descobertos;
- preservar confidencialidade de credenciais, segredos e payloads;
- impedir que o serviço seja usado como proxy para redes internas;
- limitar o custo imposto por uma credencial, evento ou destino;
- permitir autenticação da entrega e detecção de replay no consumidor;
- tornar ações privilegiadas atribuíveis sem registrar material secreto;
- falhar de forma fechada quando autenticação, resolução, configuração ou dependências forem ambíguas.

## 3. Achados priorizados

### Bloqueadores — P0

| ID | Achado | Impacto | Recomendação obrigatória |
|---|---|---|---|
| SR-001 | O PRD exige isolamento, mas não determina que o tenant seja obtido exclusivamente da API key nem que toda consulta/mutação inclua `workspace_id`. | IDOR e vazamento entre workspaces. | Aplicar `workspace_id` autenticado em toda chave, consulta, constraint e transição; recursos de outro tenant devem ser indistinguíveis de inexistentes. |
| SR-002 | O ciclo de vida da API key está incompleto: não há expiração opcional, revogação imediata, limite de credenciais, autenticação somente via header, nem proteção explícita contra comparação temporal e tentativa em massa. | Uso prolongado de credencial vazada, brute force e segredo em logs/URLs. | Definir emissão, armazenamento, apresentação, revogação, expiração, rotação, rate limit e auditoria. Nunca aceitar chave em query string ou corpo. |
| SR-003 | O formato assinado, tolerância temporal, identificação de versão e comportamento durante rotação HMAC não estão definidos. | Assinaturas incompatíveis, downgrade e replay válido indefinidamente. | Versionar o esquema; assinar timestamp, identificadores estáveis e corpo bruto; definir janela anti-replay e sobreposição de rotação verificável. |
| SR-004 | “Validar no momento da conexão” não basta se o transporte resolver o hostname novamente. Também faltam regras para proxy, todos os registros A/AAAA, IPs alternativos e formatos ambíguos. | SSRF por DNS rebinding, redirect, proxy ou bypass de parsing. | Resolver, validar e fixar o IP usado pelo `DialContext`; preservar hostname para SNI/certificado; bloquear qualquer conjunto com IP proibido; desabilitar proxy implícito e redirects por padrão. |
| SR-005 | Retenção e exclusão de payloads, respostas e auditoria estão postergadas, embora o MVP persista conteúdo potencialmente sensível. | Violação de minimização, exposição em backups e incapacidade de apagar dados. | Definir prazos, purge verificável, comportamento de backup e exclusão por workspace antes do schema. |
| SR-006 | Não existem quotas para eventos, endpoints, fan-out, replay, consultas ou chaves, nem rate limits por origem/credencial/workspace. | DoS, amplificação de um evento em muitas entregas e custo não limitado. | Criar limites multicamada com resposta `429`, sem persistência parcial, paginação limitada e métricas de rejeição. |
| SR-007 | Segredos HMAC precisam ser recuperados para assinar, mas o PRD só proíbe sua exibição; não define proteção em repouso nem separação da chave de criptografia. | Dump do banco permite forjar webhooks. | Criptografar segredos de forma autenticada com chave fora do banco e versão de chave; restringir descriptografia ao worker/caso de uso necessário. |
| SR-008 | Endpoints de métricas, traces, probes e profiling não têm política de exposição. | Vazamento operacional e superfície administrativa pública. | Separar listener/rede operacional ou exigir autenticação; profiling desligado por padrão; probes não retornam detalhes sensíveis. |

### Alta prioridade — P1

| ID | Achado | Impacto | Recomendação obrigatória |
|---|---|---|---|
| SR-009 | O replay é auditável, mas não é idempotente e não possui limites ou proteção contra concorrência. | Replays duplicados e amplificação deliberada ou acidental. | Exigir chave de idempotência para replay, motivo limitado, autorização, rate limit e uma única transição válida por comando. |
| SR-010 | A consulta de timeline pode retornar fragmentos de resposta; “payload confidencial não solicitado” é ambíguo e não existe escopo para ler payload. | Exfiltração por API de leitura e vazamento de tokens devolvidos pelo destino. | O MVP não retorna payload nem corpo de resposta; expõe apenas metadados e preview sanitizado, truncado e explicitamente permitido. |
| SR-011 | O registro “append-only” é apenas uma convenção de produto; faltam ator, resultado, origem e restrição de alteração. | Auditoria incompleta ou facilmente adulterável. | Definir eventos de auditoria estruturados, inserção exclusiva, retenção e acesso restrito; nenhuma API altera ou apaga eventos isoladamente. |
| SR-012 | O pipeline cita análise de vulnerabilidades, mas não fixa artefatos, ações, imagem, SBOM ou política de exceção. | Supply-chain não reproduzível e vulnerabilidade conhecida aceita indefinidamente. | Fixar dependências/ações, gerar SBOM, imagem mínima não-root, verificar artefato e exigir exceção com responsável e validade. |
| SR-013 | Configuração e segredos de execução não têm contrato de origem, precedência, validação ou rotação. | Inicialização insegura, segredo em repositório e fallback fraco. | Validar configuração no startup, falhar fechado, nunca aceitar default produtivo para segredos e documentar fontes autorizadas. |
| SR-014 | Erros acionáveis, headers e valores persistidos podem refletir entrada não confiável. | Enumeração, log injection, CRLF e exposição de detalhes internos. | Respostas públicas estáveis e mínimas; logs estruturados sem concatenação; sanitizar/truncar valores e nunca refletir resposta bruta do destino. |
| SR-015 | HTTPS é exigido, mas faltam requisitos de validação TLS, redirects e headers de saída. | Downgrade, interceptação e propagação acidental de credenciais. | Validar cadeia/hostname com store confiável, não permitir skip-verify, não encaminhar headers de entrada e rejeitar caracteres inválidos. |
| SR-016 | O PRD não define restauração, acesso e expurgo de backups contendo payloads/segredos. | Dados “apagados” permanecem recuperáveis e dump amplia impacto. | Backups criptografados, acesso mínimo, retenção alinhada e exclusão por expiração documentada/testada. |

### Endurecimento — P2

| ID | Achado | Impacto | Recomendação |
|---|---|---|---|
| SR-017 | Não há política de clock para timestamp de assinatura, leases e auditoria. | Falsos replays, leases incorretos e timeline inconsistente. | Usar UTC, monitorar desvio e testar clock skew; não depender do relógio do cliente para autorização. |
| SR-018 | Não está definido se endpoints são revalidados periodicamente sem uma tentativa. | Endpoint antes público pode mudar para destino proibido. | Revalidar em cada conexão; opcionalmente desabilitar após falhas de política repetidas e auditar a decisão. |
| SR-019 | Não há política explícita para CORS, métodos e content types. | Superfície HTTP maior que a necessária. | CORS desabilitado por padrão, allowlist de métodos/content types e headers de segurança adequados à API. |
| SR-020 | Não há processo de resposta a incidente, embora o PRD proíba declarar compliance. | Revogação, comunicação e preservação de evidência improvisadas. | Incluir runbook mínimo para vazamento de API key/HMAC, SSRF e exposição de payload antes de hospedar terceiros. |

## 4. Requisitos de segurança obrigatórios

Os requisitos abaixo complementam `FR-*` e `NFR-*`. Eles devem aparecer por referência na arquitetura, no schema, no backlog e nos testes.

### 4.1 Identidade, autenticação e autorização

#### SEC-001 — Contexto de tenant confiável

O `workspace_id` de cada operação deve ser derivado exclusivamente da API key autenticada. IDs ou headers de workspace fornecidos pelo cliente não podem selecionar nem substituir o tenant.

**Critérios de aceite:**

- uma chave do workspace B não lê, altera, desabilita, rotaciona, reenvia ou confirma a existência de recurso do workspace A;
- consultas e mutações de recurso incluem simultaneamente seu ID e o `workspace_id` autenticado;
- testes cobrem IDs válidos de outro tenant em todas as rotas com recurso;
- respostas para recurso ausente e recurso de outro tenant são indistinguíveis em status e formato.

#### SEC-002 — API keys de alta entropia e apresentação única

As chaves devem conter ao menos 256 bits gerados por CSPRNG. O token completo é exibido uma única vez e transmitido somente no header `Authorization`; query string, cookie e corpo são rejeitados como meios de autenticação.

**Critérios de aceite:**

- o banco contém apenas prefixo não secreto e verificador criptográfico, nunca o token completo;
- comparação do verificador é resistente a timing e não diferencia “prefixo inexistente” de “token inválido” externamente;
- criação de chave nunca registra token em log, trace, métrica, erro ou auditoria;
- endpoints autenticados rejeitam transporte sem TLS fora do modo local explicitamente isolado.

#### SEC-003 — Ciclo de vida e menor privilégio da API key

Cada chave deve ter ID, workspace, escopos, data de criação, estado, expiração opcional e instante de revogação. Revogação tem efeito em novas requisições sem período de tolerância. Deve existir limite configurável de chaves ativas por workspace.

**Critérios de aceite:**

- chave revogada ou expirada recebe `401` e não altera estado;
- falta de escopo recebe `403` somente depois de autenticação válida;
- emissão, rotação e revogação são auditadas sem material secreto;
- a chave `admin` não é usada automaticamente em quickstart ou exemplos quando um escopo menor basta.

#### SEC-004 — Defesa contra abuso de autenticação

Falhas de autenticação devem sofrer limite por origem e prefixo, com resposta genérica. Contadores e alertas não podem usar a chave ou IP bruto como label de métrica.

**Critérios de aceite:**

- rajadas acima do limite documentado recebem `429` sem consulta ou escrita desnecessária no banco;
- tempos e corpos de resposta não permitem enumerar prefixos válidos de maneira trivial;
- testes confirmam que o limite não bloqueia permanentemente uma credencial legítima.

### 4.2 Assinatura, rotação e replay

#### SEC-005 — Esquema de assinatura versionado

Cada entrega deve enviar versão do esquema, timestamp UTC, `event_id`, `delivery_id` e uma ou mais assinaturas HMAC-SHA256. A entrada canônica deve incluir, sem ambiguidades, versão, timestamp, IDs e os bytes exatos do corpo.

**Critérios de aceite:**

- a OpenAPI e o exemplo consumidor definem nomes de headers, codificação e string canônica byte a byte;
- alterar corpo, timestamp, IDs ou versão invalida a assinatura;
- a verificação de exemplo usa comparação constante e rejeita algoritmo/versão desconhecidos;
- nenhuma normalização/re-serialização do JSON ocorre entre cálculo e envio.

#### SEC-006 — Janela anti-replay

O consumidor de referência deve rejeitar timestamp fora de uma tolerância padrão documentada e deduplicar pelo `delivery_id`. A documentação deve esclarecer que HMAC autentica origem e integridade, mas não garante entrega única.

**Critérios de aceite:**

- mensagem capturada e reapresentada fora da janela é rejeitada;
- repetição dentro da janela pode ser identificada pelo mesmo `delivery_id`;
- desvio de relógio aceito é configurável dentro de teto seguro e testado nos dois sentidos.

#### SEC-007 — Rotação HMAC sem interrupção nem downgrade

A rotação deve criar uma nova versão de segredo, manter a anterior somente durante janela limitada e indicar `key_id` não secreto em cada assinatura. O comportamento de tentativas já agendadas deve ser determinístico. A arquitetura deve optar por assinatura dupla durante a sobreposição ou uso explícito da versão vinculada à entrega.

**Critérios de aceite:**

- durante a sobreposição, o receptor que conhece a versão documentada valida novas entregas;
- após o prazo, o segredo anterior deixa de ser utilizado e seu ciphertext é expurgado quando não necessário para histórico;
- rotação concorrente não produz tentativa sem uma versão válida;
- downgrade para versão aposentada é recusado e auditado.

### 4.3 Segredos e dados sensíveis

#### SEC-008 — Criptografia autenticada de segredos recuperáveis

Segredos HMAC devem ser cifrados em repouso com algoritmo autenticado. A chave mestra não pode residir na mesma base nem no repositório, deve possuir versão e permitir rotação. Apenas o worker e o caso de uso de rotação podem solicitar descriptografia.

**Critérios de aceite:**

- dump isolado do PostgreSQL não contém segredo HMAC utilizável;
- ciphertext ou metadado adulterado falha fechado;
- valor descriptografado não sobrevive além do escopo da tentativa nem é incluído em erros;
- ambiente de produção não inicia sem origem explícita de chave mestra válida.

#### SEC-009 — Minimização de payload e resposta

Payloads são tratados como sensíveis. A API de consulta do MVP não retorna corpo do evento. Corpo de resposta do destino não é persistido por padrão; quando necessário ao diagnóstico, somente preview sanitizado e truncado conforme limite pequeno documentado.

**Critérios de aceite:**

- payload completo não aparece em consultas, logs, traces, métricas, auditoria ou panic report;
- headers de resposta como `Set-Cookie`, `Authorization` e tokens conhecidos nunca são persistidos;
- testes com canários secretos falham se o valor surgir em qualquer sink de observabilidade.

#### SEC-010 — Retenção e purge

Antes do schema, devem ser definidos prazos padrão e máximos para payload, preview de resposta, tentativas e auditoria. O purge deve ser automático, idempotente e observável, preservando apenas metadados mínimos quando exigido para integridade operacional.

**Critérios de aceite:**

- dados vencidos deixam a base ativa dentro de SLA documentado;
- falhas do purge geram métrica/alerta sem registrar conteúdo;
- replay após expurgo de payload é recusado com estado explícito, não recria conteúdo inexistente;
- testes com relógio controlado comprovam retenção por categoria.

#### SEC-011 — Exclusão por workspace e backups

Deve existir procedimento administrativo documentado para excluir dados de um workspace, revogar credenciais e impedir novos envios. Backups são cifrados, têm acesso mínimo e expiram conforme política divulgada; exclusão imediata de cópias imutáveis não deve ser prometida.

**Critérios de aceite:**

- exclusão remove ou anonimiza dados ativos sem quebrar isolamento de outros tenants;
- credenciais e endpoints do workspace deixam de funcionar antes do purge assíncrono;
- runbook informa prazo máximo para saída dos backups;
- restauração de backup não reativa credencial já revogada sem reconciliação segura.

### 4.4 SSRF e segurança do cliente HTTP

#### SEC-012 — Canonicalização e política de URL

Somente URLs absolutas `https` são aceitas fora do modo local. Userinfo, fragmento, hostname vazio, porta inválida, caracteres de controle, zona IPv6 e representações ambíguas de IP devem ser rejeitados. Hostnames são normalizados antes da política.

**Critérios de aceite:**

- suíte cobre IPv4/IPv6, IPv4-mapped IPv6, loopback, private, link-local, multicast, unspecified, benchmark/documentation/reserved e metadata;
- nomes equivalentes, maiúsculas, ponto final e IDN não contornam a política;
- HTTP local exige flag explícita indisponível na configuração produtiva.

#### SEC-013 — Resolução e conexão sem TOCTOU

Para cada tentativa, o serviço deve resolver A/AAAA com timeout, validar todos os resultados e conectar apenas a um IP validado, mantendo o hostname original para SNI e validação do certificado. O transporte não pode fazer segunda resolução implícita.

**Critérios de aceite:**

- qualquer IP proibido no conjunto resolvido causa recusa, conforme política fail-closed documentada;
- teste de DNS rebinding entre validação e conexão não alcança endereço proibido;
- falha/timeout de DNS é transitória limitada e nunca cai para resolver alternativo inseguro;
- `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY` do ambiente não alteram a rota do cliente de entrega.

#### SEC-014 — Redirects e TLS

Redirects de webhooks ficam desabilitados no MVP. TLS deve validar certificado e hostname com raízes confiáveis; `InsecureSkipVerify` ou equivalente não pode existir em configuração produtiva.

**Critérios de aceite:**

- respostas 3xx são classificadas pela política documentada sem nova chamada automática;
- certificado expirado, hostname incorreto ou cadeia inválida falha fechado;
- versões/protocolos criptográficos obsoletos não são habilitados deliberadamente;
- o Chaos Lab pode usar confiança local explícita sem reduzir o comportamento produtivo.

#### SEC-015 — Limites do transporte de saída

O cliente deve possuir limites independentes para DNS, conexão, handshake TLS, headers e duração total; limitar headers e bytes lidos; fechar resposta; não encaminhar credenciais nem headers recebidos do produtor.

**Critérios de aceite:**

- servidor lento, resposta infinita, headers excessivos e conexão que nunca completa liberam slot e recursos no prazo;
- memória e goroutines permanecem dentro do limite em teste prolongado;
- headers de saída pertencem a allowlist construída pelo serviço e rejeitam CR/LF;
- IP, URL completa e query sensível não aparecem em labels de métricas.

### 4.5 Disponibilidade, quotas e replay operacional

#### SEC-016 — Rate limits e quotas multicamada

Devem existir limites configuráveis por API key/workspace e globais para ingestão, mutação de endpoint, rotação, consulta e replay. Também devem existir cotas de endpoints ativos, tamanho da chave de idempotência, tipos de evento e entregas criadas por evento.

**Critérios de aceite:**

- violação retorna `429` ou erro de quota documentado antes de trabalho caro e sem persistência parcial;
- um workspace saturado não impede progresso de outro em teste de isolamento;
- reinício do processo não transforma limite de segurança documentado em bypass silencioso;
- limites e comportamento distribuído do MVP são declarados honestamente.

#### SEC-017 — Fan-out e paginação limitados

Um evento não pode criar número ilimitado de entregas. Listagens exigem paginação com tamanho máximo, ordenação estável e cursor opaco; filtros têm tamanho/complexidade limitados.

**Critérios de aceite:**

- exceder fan-out máximo rejeita o evento antes do commit;
- nenhuma rota de listagem retorna coleção ilimitada;
- cursor de outro workspace é inválido e não revela dados;
- consultas adversariais permanecem limitadas por timeout e índices previstos.

#### SEC-018 — Replay idempotente e autorizado

Replay exige `deliveries:retry`, motivo sanitizado e limitado, `Idempotency-Key`, rate limit e transição atômica. Entrega bem-sucedida, payload expurgado ou recurso de outro tenant não pode ser reenviado.

**Critérios de aceite:**

- comandos concorrentes com a mesma chave geram uma única ação de replay;
- a mesma chave com parâmetros diferentes retorna conflito;
- auditoria registra ator, entrega, resultado, motivo sanitizado e request ID;
- replay não apaga, reordena nem reescreve tentativas anteriores.

### 4.6 Auditoria, observabilidade e operação

#### SEC-019 — Auditoria estruturada de ações sensíveis

Criação/revogação de chave, criação/alteração/desativação de endpoint, rotação HMAC, replay, mudança de política e purge devem gerar registro append-only com ator técnico, workspace, ação, recurso, instante do servidor, request ID e resultado. Segredos e payloads são proibidos.

**Critérios de aceite:**

- API da aplicação não oferece update/delete de evento de auditoria individual;
- falhas de autorização relevantes são contabilizadas sem permitir log flood ilimitado;
- consultas à auditoria respeitam tenant e escopo administrativo definido;
- alteração direta privilegiada no banco é considerada risco residual e consta do threat model.

#### SEC-020 — Telemetria segura

Logs são estruturados, escapam entrada não confiável e aplicam allowlist de campos. Traces não carregam payload, secret, API key, assinatura ou URL completa. Exportadores usam autenticação/TLS fora do ambiente local.

**Critérios de aceite:**

- canários em payload, headers, query, resposta e segredo não surgem na telemetria;
- erro público não contém SQL, stack trace, endereço interno ou resposta bruta do destino;
- cardinalidade de métricas permanece limitada sob IDs aleatórios;
- panic é recuperado na fronteira HTTP, correlacionado e sanitizado sem mascarar encerramento seguro.

#### SEC-021 — Superfície operacional separada

Métricas, health, debug e profiling não compartilham exposição pública sem controle explícito. Readiness/liveness retornam apenas estado mínimo; profiling fica desabilitado por padrão.

**Critérios de aceite:**

- configuração padrão não expõe `pprof`, variáveis, goroutines, SQL ou configuração;
- listener operacional usa bind/rede separada ou autenticação apropriada;
- readiness negativa não revela credenciais, DSN, host interno ou erro bruto;
- Chaos Lab e endpoints destrutivos não estão presentes na imagem/configuração de produção.

#### SEC-022 — Configuração e falha fechada

Toda configuração de segurança deve ter tipo, faixa válida, fonte e precedência documentados. Configuração inválida, chave ausente, algoritmo desconhecido ou modo local em ambiente produtivo devem impedir startup.

**Critérios de aceite:**

- não existem segredos reais, `.env` ou certificados privados versionados;
- logs de startup mostram apenas nomes/versões não secretas das configurações;
- defaults não desabilitam TLS, autenticação, SSRF, limites ou criptografia em produção;
- shutdown não gera dump de configuração ou conteúdo em erro.

### 4.7 Supply chain e entrega

#### SEC-023 — Dependências e automação reproduzíveis

Versão de Go, módulos, ferramentas e automações de CI devem ser fixados. O pipeline executa testes, race detector, vet, análise estática, `govulncheck`, scanner de imagem e detecção de segredos.

**Critérios de aceite:**

- `go.mod`/`go.sum` e ferramentas pinadas reproduzem o build;
- ações de CI de terceiros usam commit imutável ou política equivalente;
- achado High/Critical bloqueia release, salvo exceção com justificativa, responsável e data de expiração;
- pull request de dependência exibe mudança verificável e não executa segredo em contexto não confiável.

#### SEC-024 — Artefato mínimo e verificável

A imagem final deve ser mínima, executar como usuário não-root, sem toolchain/segredos, com filesystem somente leitura quando compatível. Release gera SBOM e associa o artefato ao commit de origem.

**Critérios de aceite:**

- inspeção da imagem não encontra fonte desnecessária, `.git`, credenciais ou arquivos de desenvolvimento;
- processo não exige root nem capacidade Linux adicional;
- SBOM é gerado no CI e scanner avalia exatamente a imagem publicada;
- documentação explica verificação e atualização de dependências.

#### SEC-025 — Runbook de incidente mínimo

Antes de qualquer hospedagem com dados de terceiros, deve existir runbook para vazamento de API key, comprometimento de segredo HMAC, SSRF confirmado, exposição de payload e dependência crítica.

**Critérios de aceite:**

- cada cenário define contenção, revogação/rotação, evidências, recuperação e comunicação;
- exercícios locais demonstram revogação de chave e rotação HMAC sem editar banco manualmente;
- limitações de compliance e prazo de retenção são comunicados sem alegação indevida.

## 5. Matriz de rastreabilidade

| Área do produto | Requisitos de segurança | Histórias afetadas |
|---|---|---|
| API key e tenancy | SEC-001–SEC-004 | US-001, US-002, US-003, US-010, US-011 |
| HMAC e replay | SEC-005–SEC-007 | US-003, US-005, US-014, US-015 |
| Segredos e payloads | SEC-008–SEC-011 | US-003, US-004, US-005, US-010, US-013 |
| SSRF e HTTP de saída | SEC-012–SEC-015 | US-002, US-005, US-007, US-008, US-014 |
| DoS e recuperação | SEC-016–SEC-018 | US-004, US-008, US-010, US-011 |
| Operação e auditoria | SEC-019–SEC-022 | US-003, US-011, US-012, US-013, US-014 |
| Supply chain e incidente | SEC-023–SEC-025 | US-015 e Definition of Done |

## 6. Decisões e bloqueios para a arquitetura

A etapa de arquitetura pode prosseguir, mas não pode ser considerada concluída sem resolver e registrar em ADR ou seção equivalente:

1. **Tenant enforcement:** padrão obrigatório para carregar/mutar recursos com `workspace_id`; estratégia de teste de isolamento; decisão sobre defesa adicional no banco, sem depender apenas do handler.
2. **Verificador de API key:** formato, prefixo, função criptográfica/verificador, comparação, revogação, cache e comportamento entre réplicas.
3. **Envelope de segredos:** algoritmo AEAD, origem e versão da chave mestra, rotação, limites de acesso e comportamento no desenvolvimento local.
4. **Protocolo HMAC:** canonicalização, headers, tolerância de relógio, rotação, assinatura dupla ou versão vinculada, e exemplo interoperável.
5. **Cliente HTTP anti-SSRF:** parser, classificação completa de IP, resolver, `DialContext` que fixa IP, SNI, redirects, proxy e testes de rebinding.
6. **Políticas de dados:** prazos concretos por categoria, purge, exclusão de workspace, backups e impossibilidade de replay depois do expurgo.
7. **Quotas/rate limiting:** dimensões, limites padrão, comportamento com uma ou várias instâncias e mecanismo que não exija Redis no MVP.
8. **Auditoria:** schema append-only, eventos obrigatórios, acesso, retenção e distinção entre auditoria e logs operacionais.
9. **Superfície operacional:** listeners, rede/autenticação, probes, métricas, tracing, profiling e separação do Chaos Lab.
10. **Supply chain/deploy:** imagem mínima, usuário, filesystem, pinagem, SBOM, scanners e política de exceção.

Decisões de produto ainda necessárias, mas que podem receber defaults seguros no planejamento técnico:

- retenção padrão e máxima de payload/tentativas/auditoria;
- janela de sobreposição HMAC e tolerância anti-replay;
- limites padrão de endpoints, fan-out, ingestão, consultas e replays;
- se dados ativos de payload serão cifrados em nível de aplicação no MVP hospedado ou protegidos por criptografia de infraestrutura com modelo de ameaça documentado.

Até essas decisões serem fixadas, o serviço é adequado para laboratório local com dados sintéticos, não para receber payload real de terceiros.

## 7. Evidências exigidas no backlog e na Definition of Done

- testes negativos de autorização em todas as rotas com dois workspaces;
- teste de API key revogada/expirada e ausência de credenciais na telemetria;
- vetores de assinatura publicados e usados por implementação independente;
- testes de adulteração, timestamp expirado, rotação e deduplicação;
- suíte SSRF com IPv4/IPv6, redirects, proxy, DNS rebinding, SNI e respostas lentas/excessivas;
- teste de fan-out, paginação, rate limit e isolamento entre tenants sob saturação;
- teste de purge e impossibilidade de replay após exclusão do payload;
- canários automatizados para vazamento em logs, traces, métricas, auditoria e erros;
- inspeção automatizada de imagem, execução não-root, SBOM e scanners;
- threat model e runbook atualizados antes da demonstração pública.

## 8. Riscos residuais

Mesmo após implementar os requisitos, permanecem riscos que devem ser declarados:

| ID | Risco residual | Tratamento/aceitação |
|---|---|---|
| RR-001 | `At-least-once` permite duplicação legítima após timeout ambíguo. | Aceito; IDs estáveis, janela anti-replay e documentação de deduplicação no consumidor. |
| RR-002 | Um destino público comprometido pode armazenar ou vazar payload recebido. | Aceito pelo modelo; minimizar payload e deixar a responsabilidade do destino explícita. |
| RR-003 | Credencial de banco altamente privilegiada pode ler/alterar metadados e auditoria; com chave mestra do runtime, pode alcançar segredos. | Minimizar privilégios, separar chaves, registrar acesso e considerar KMS/HSM em evolução. |
| RR-004 | Bloqueios de IP podem produzir falso positivo ou negativo em faixas novas/especiais. | Manter política testada/atualizada, egress de rede como defesa adicional e falha fechada. |
| RR-005 | Rate limit local não é perfeitamente global ao escalar múltiplas instâncias. | Documentar limite do MVP; adotar coordenação persistente antes de prometer quota global estrita. |
| RR-006 | Dados excluídos persistem até expiração de backups imutáveis. | Declarar prazo, controlar restauração e não prometer apagamento instantâneo. |
| RR-007 | Telemetria pode inferir volume e padrão operacional mesmo sem payload. | Restringir acesso, retenção e exportação; aceitar como necessidade operacional residual. |
| RR-008 | Dependência sem vulnerabilidade conhecida ainda pode conter falha desconhecida. | Reduzir dependências, atualizar continuamente, SBOM e resposta a incidente. |

## 9. Gate de saída desta revisão

O PRD está apto a seguir para Design & UI e System Architecture com as seguintes condições:

- `SEC-001` a `SEC-025` são requisitos normativos do MVP, ainda que implementados em cortes progressivos;
- os dez bloqueios arquiteturais da seção 6 são respondidos antes do início da Sprint 1;
- arquitetura e schema recebem revisões de segurança próprias, conforme o fluxo `new-project`;
- qualquer adiamento de requisito P0 precisa aparecer como risco explícito e restringir o uso a dados sintéticos/local, sem ser tratado como conformidade de produção.

Este parecer não certifica LGPD, PCI DSS, HIPAA, SOC 2 ou qualquer outro regime. Ele define o baseline de segurança do produto de portfólio e os controles mínimos antes de uma eventual hospedagem pública.
