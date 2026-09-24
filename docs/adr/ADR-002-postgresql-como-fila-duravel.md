# ADR-002: PostgreSQL como fonte de verdade e fila durável

## Status: Accepted

## Contexto

Evento e todas as entregas precisam existir antes do `202`, e o MVP deve operar sem Redis/Kafka. Claims concorrentes não podem bloquear a fila inteira.

## Decisão

Usar PostgreSQL 17 para dados e fila. Ingestão grava evento e fan-out na mesma transação. Worker reserva batches limitados com `FOR UPDATE SKIP LOCKED`, atualiza lease/fencing e confirma antes de qualquer HTTP. Elegibilidade e transições são updates condicionais. Polling com jitter será usado inicialmente.

## Alternativas descartadas

- Kafka/Redis: adicionam componente e dual-write sem necessidade demonstrada.
- Fila apenas em memória: perde trabalho em crash.
- HTTP dentro da transação: mantém locks durante I/O hostil.

## Consequências

Consistência e operação ficam simples e mensuráveis. A fila compete por recursos do banco e exige índices, batches curtos e benchmarks. Broker só será revisto com evidência de gargalo.

