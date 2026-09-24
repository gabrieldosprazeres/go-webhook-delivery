# ADR-001: Monólito modular com binários API, worker e Chaos Lab

## Status: Accepted

## Contexto

O produto precisa aceitar eventos e executar entregas com perfis de escala e privilégio diferentes, mas o MVP não justifica contratos de rede, deploy e dados de vários microsserviços. O Chaos Lab é necessário para demonstração, porém não pode ampliar a superfície produtiva.

## Decisão

Manter um módulo Go e um repositório, organizados por capacidades, com composition roots em `cmd/api`, `cmd/worker` e `cmd/chaoslab`. API e worker compartilham código, usam processos e roles de banco diferentes. Chaos Lab tem imagem/profile local separado e não integra o artefato produtivo.

## Alternativas descartadas

- Microsserviços independentes: aumentam operação e consistência distribuída sem dor medida.
- Um único processo API+worker: mistura ciclos de vida, escala e privilégios.
- Chaos endpoints dentro da API: cria superfície destrutiva em produção.

## Consequências

Deploy e entendimento permanecem simples, enquanto API e worker escalam separadamente. Há acoplamento de release intencional. Extração futura exige métrica, propriedade de dados e novo ADR.

