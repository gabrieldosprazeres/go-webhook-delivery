# ADR-004: Leases e fencing para coordenação de workers

## Status: Accepted

## Contexto

Workers podem morrer, pausar ou concluir depois que outro processo recuperou a mesma entrega. Lease sozinho não impede um worker obsoleto de sobrescrever o resultado novo.

## Decisão

Cada claim define owner, expiração e incrementa um fencing token monotônico. Finalização exige ID, tenant, estado, owner, token vigente e lease não expirado na mesma atualização. Zero linhas afetadas significa resultado obsoleto. HTTP ocorre após commit. Defaults: tentativa 10 s, lease 30 s; configuração exige margem mínima de 10 s.

## Alternativas descartadas

- Lock PostgreSQL durante HTTP: reduz disponibilidade e mantém transação longa.
- Lease sem fencing: admite finalização tardia.
- Lock distribuído externo: adiciona componente sem eliminar a necessidade de estado condicional.

## Consequências

Crash se recupera por TTL e resultados tardios são descartados. Estados/attempts precisam registrar token e testes determinísticos. Renovação de lease fica adiada até haver necessidade real.

