# ADR-013: Observabilidade privada e OTLP na rede Docker

## Status: Accepted

## Contexto

API e worker já exportam Prometheus e OTLP, mas o deploy de demonstração não materializa Collector, Prometheus, Tempo ou Grafana. Produção rejeita OTLP sem TLS, enquanto todos esses processos residem no mesmo daemon Docker da VPS.

## Decisão

Provisionar Collector, Prometheus, Tempo e Grafana em `telemetry-private`, rede Docker com `internal: true`. Collector, Prometheus, Tempo e listeners `9090–9092` não publicam portas nem recebem domínio. Grafana permanece autenticado e acessível ao operador somente por loopback/túnel SSH.

O bind do Grafana é `127.0.0.1:33000`; ele não participa da rede de ingress do
EasyPanel. O navegador usa `http://localhost:33000` sobre o túnel SSH, por isso o cookie
do Grafana não usa `Secure`, mas conserva `HttpOnly` do próprio Grafana e
`SameSite=Strict`. Essa exceção não autoriza HTTP ou cookies sem `Secure` em qualquer
hostname público.

OTLP/HTTP sem TLS é aceito em produção apenas quando `WDE_OTEL_ALLOW_PRIVATE_HTTP=true` e o endpoint é exatamente `http://otel-collector:4318`. Qualquer outro HTTP continua fail-closed. Ao mover o Collector para outro host, HTTPS/mTLS volta a ser obrigatório.

Retenção inicial: Prometheus 7 dias/512 MB. O Tempo retém até 24 horas em `tmpfs`
de 256 MiB, com ingestão local limitada a 1 MiB/s, burst de 2 MiB e trace individual
de até 1 MiB. A regra `WDETempoDiscardingSpans` sinaliza no Prometheus quando qualquer
limite começa a descartar spans. Telemetria é descartável, pode ser perdida no restart
e sua indisponibilidade nunca bloqueia ingestão ou entrega.

## Alternativas descartadas

- Expor Collector pelo proxy para obter TLS: cria uma superfície pública desnecessária.
- PKI interna para containers no mesmo host: complexidade desproporcional nesta VPS.
- Grafana anônimo: vaza topologia, traces e informação operacional.

## Consequências

O risco residual de tráfego claro existe somente dentro do daemon já privilegiado.
Configuração, regras, dashboards e datasources ficam versionados. O volume Prometheus
e o volume Grafana não entram no backup crítico; traces do Tempo são explicitamente
efêmeros e possuem limite físico de armazenamento.
