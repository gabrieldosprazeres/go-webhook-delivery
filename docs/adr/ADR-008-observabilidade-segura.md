# ADR-008: Observabilidade segura e superfície operacional separada

## Status: Accepted

## Contexto

O sistema precisa ser diagnosticável, mas payloads, segredos, URLs e IDs em labels geram vazamento e cardinalidade. Probes/debug na porta pública ampliam exposição.

## Decisão

Usar `slog` JSON, OpenTelemetry e Prometheus com allowlist de campos. Payload, key, HMAC, assinatura, headers, query, DSN e resposta bruta são proibidos. IDs detalhados ficam em logs/traces; métricas usam dimensões limitadas. Cada processo possui listener operacional interno/loopback; probes revelam somente estado mínimo e `pprof` fica desligado por padrão.

## Alternativas descartadas

- Logar requests/responses inteiros: vazamento previsível.
- IDs/URLs como labels: cardinalidade ilimitada.
- Health/métricas na porta pública: superfície administrativa desnecessária.

## Consequências

Diagnóstico depende de correlação por request/trace/delivery IDs e acesso controlado. Canários automatizados verificam todos os sinks. Exportador indisponível não interrompe entrega.

## Implementação no MVP

- API e worker possuem registry Prometheus por processo, servido apenas em `GET /metrics` no listener operacional.
- Labels são conjuntos fechados: rota conhecida, método bounded, classe HTTP, outcome e categoria allowlisted. IDs existem somente como atributos de trace.
- OTLP/HTTP é opcional e no-op por padrão. O processo mantém provider próprio, não depende de globals e limita export/flush a 10 segundos; falha do collector não altera o caminho de negócio.
- Apenas `traceparent` W3C válido é propagado. `tracestate` e baggage recebidos não atravessam a fronteira, e nenhum header/corpo/URL é convertido em atributo.
- O bit `sampled` de parent remoto não é confiável: parents remotos sampled e unsampled passam novamente pela razão local. Autenticação da requisição não amplia amostragem.
- Transações tenant-scoped, claim e finalização têm spans próprios com resultado allowlisted; somente IDs opacos necessários à correlação são atributos.
- HTTP/core e flush OTLP compartilham um único deadline absoluto de shutdown; cada fase recebe apenas o orçamento restante e falha do flush não substitui o resultado do negócio.
- `/livez` é process-only; `/readyz` verifica role/schema lógico v5, restore quarantine e keyrings. O worker também exige scheduler ativo e retenção saudável.
- O healthcheck da imagem distroless reutiliza o próprio binário e acessa somente loopback, sem credenciais ou shell.
- A função agregada `wde.delivery_queue_metrics()` pertence a executor `NOLOGIN`, não aceita filtros/IDs e concede `EXECUTE` somente a `wde_worker`.
- Bind operacional não-loopback só é aceitável em rede privada com ACL/NetworkPolicy explícita; essa superfície não oferece autenticação de aplicação e nunca deve receber ingress público.
