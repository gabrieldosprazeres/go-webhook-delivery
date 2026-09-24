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

