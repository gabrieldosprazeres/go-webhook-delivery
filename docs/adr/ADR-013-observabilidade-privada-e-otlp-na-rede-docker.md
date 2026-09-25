# ADR-013: Observabilidade privada e OTLP na rede Docker

## Status: Accepted

## Contexto

API e worker já exportam Prometheus e OTLP, mas o deploy de demonstração não materializa Collector, Prometheus, Tempo ou Grafana. Produção rejeita OTLP sem TLS, enquanto todos esses processos residem no mesmo daemon Docker da VPS.

## Decisão

Provisionar Collector, Prometheus, Tempo e Grafana em `telemetry-private`, rede Docker com `internal: true`. Collector, Prometheus, Tempo e listeners `9090–9092` não publicam portas nem recebem domínio. Grafana permanece autenticado e acessível ao operador somente por loopback/túnel SSH.

OTLP/HTTP sem TLS é aceito em produção apenas quando `WDE_OTEL_ALLOW_PRIVATE_HTTP=true` e o endpoint é exatamente `http://otel-collector:4318`. Qualquer outro HTTP continua fail-closed. Ao mover o Collector para outro host, HTTPS/mTLS volta a ser obrigatório.

Retenção inicial: Prometheus 7 dias/512 MB e Tempo 24 horas. Telemetria é descartável e sua indisponibilidade nunca bloqueia ingestão ou entrega.

## Alternativas descartadas

- Expor Collector pelo proxy para obter TLS: cria uma superfície pública desnecessária.
- PKI interna para containers no mesmo host: complexidade desproporcional nesta VPS.
- Grafana anônimo: vaza topologia, traces e informação operacional.

## Consequências

O risco residual de tráfego claro existe somente dentro do daemon já privilegiado. Configuração, dashboards e datasources ficam versionados; volumes de telemetria não entram no backup crítico.
