# ADR-003: Semântica at-least-once e idempotência

## Status: Accepted

## Contexto

Após um timeout, o worker não sabe se o consumidor processou a requisição. `Exactly-once` ponta a ponta não é alcançável apenas pelo emissor. Produtores também precisam repetir requests incertos sem duplicar fan-out.

## Decisão

Assumir e documentar `at-least-once`. `event_id` e `delivery_id` são estáveis entre tentativas. Ingestão exige `Idempotency-Key` única por workspace e fingerprint SHA-256 do conteúdo lógico canonicalizado; repetição igual retorna o evento original e conteúdo diferente retorna `409`. Replay possui idempotência própria. Consumidores deduplicam por `delivery_id`.

## Alternativas descartadas

- Alegar exactly-once: promessa incorreta diante de timeout/crash.
- Deduplicação somente em memória: não sobrevive restart nem coordena réplicas.
- Criar novo delivery por tentativa: impede deduplicação no consumidor.

## Consequências

Duplicações físicas continuam possíveis e são parte do contrato. Canonicalização e constraints precisam de vetores/testes concorrentes. O sistema prioriza não perder evento aceito.

