# ADR-014: Métricas do tenant via PostgreSQL

## Status: Accepted

## Contexto

Clientes precisam observar suas próprias entregas, mas Prometheus e Tempo são superfícies operacionais globais. Adicionar `workspace_id` a labels cria cardinalidade sem limite e risco de vazamento entre tenants.

## Decisão

O console calcula cards e séries do workspace a partir das tabelas duráveis, sempre dentro de `tenanttx`/RLS. O workspace deriva da sessão e nunca de parâmetro do navegador. A primeira versão oferece janela fixa de 24 horas, buckets de uma hora, timeout de 2 segundos, pontos limitados e cache curto no processo.

O acesso reutiliza `deliveries:read`; não será criado `metrics:read` enquanto os agregados não incluírem billing, auditoria ou RBAC humano. Nenhuma resposta contém payload, URL, segredo, trace bruto ou identificador de outro tenant.

## Alternativas descartadas

- PromQL exposta ao cliente: consulta arbitrária e risco de isolamento.
- Grafana embutido: mistura ferramenta operacional e produto.
- Labels por tenant/recurso: cardinalidade e privacidade inadequadas.

## Consequências

PostgreSQL permanece a fonte de verdade das métricas do produto. Consultas e índices entram no gate de performance; rollup horário só será introduzido se medição real mostrar necessidade.
