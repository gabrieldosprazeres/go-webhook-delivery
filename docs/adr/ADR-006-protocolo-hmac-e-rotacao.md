# ADR-006: Protocolo HMAC versionado e rotação

## Status: Accepted

## Contexto

Consumidores precisam verificar origem/integridade e resistir a replay. O protocolo deve ser interoperável, deterministicamente rotacionável e não reserializar JSON.

## Decisão

Usar HMAC-SHA256 `v1`. A entrada é `v1\n<timestamp>\n<event_id>\n<delivery_id>\n<key_id>\n<body bruto>`, codificada em base64url sem padding. Headers carregam versão, timestamp Unix UTC, IDs e pares key/signature. Tolerância padrão é ±5 min, teto ±15 min. Rotação assina com chave nova e anterior por 24 h (máximo 7 dias); seleção do keyring é atômica por tentativa.

## Alternativas descartadas

- Assinar JSON reserializado: introduz diferenças de canonicalização.
- Troca instantânea: quebra consumidores durante rotação.
- Segredo estático sem key ID: impede rotação verificável.

## Consequências

Serão publicados vetores independentes, testes de adulteração/clock/rotação e exemplo constant-time. A assinatura autentica e protege integridade, mas deduplicação continua responsabilidade do consumidor.

## Implementação

Materializada na Sprint 4 com comando idempotente e serialização endpoint-scoped. Uma única versão `retiring` pode coexistir com a `active`; claims carregam snapshot atômico das duas, o header usa pares separados por `;` e o segredo anterior é purgado por job limitado após `retire_at`. O purge criptográfico preserva hash/fingerprint do comando até `metadata_retention_days`: retry idêntico após o envelope desaparecer retorna `rotation_result_expired`, enquanto fingerprint divergente retorna conflito, ambos sem nova versão ou auditoria.
