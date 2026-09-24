# ADR-009: Criptografia e retenção de payloads e segredos

## Status: Accepted

## Contexto

O worker precisa recuperar HMAC secrets e payloads, que podem ser sensíveis. Criptografia somente do disco não protege um dump do PostgreSQL. Retenção indefinida aumenta impacto.

## Decisão

Cifrar payloads e HMAC secrets no nível da aplicação com AES-256-GCM e keyrings separados/versionados fora do banco. AAD vincula versão, workspace, tipo e recurso. Produção lê KEKs de secret files; API keys são não recuperáveis. Payload: 30 dias default/90 máximo; preview off (se habilitado, 2 KiB/7 dias); metadados 90/180 dias; auditoria 365/730; backups cifrados 35 dias. Purge horário remove vencidos em até 24 h.

## Alternativas descartadas

- Somente criptografia de volume: dump contém plaintext utilizável.
- Guardar secrets com hash: worker precisa recuperá-los para assinar.
- Retenção indefinida: viola minimização e amplia incidentes.

## Consequências

Runtime precisa proteger e rotacionar KEKs; key version ausente falha fechado. Replay após purge é impossível. Backups imutáveis expiram, mas exclusão instantânea deles não é prometida. KMS/HSM fica para hospedagem real/compliance.

## Implementação

Materializada na Sprint 4 com envelope v2 e AAD tipado, leitura legada v1 para upgrade e constraints temporais/all-or-none que rejeitam `NULL/UNKNOWN`. Entry points de manutenção rejeitam lote nulo ou fora de `1..1000`. O ciclo horário drena batches por progresso dentro de budget, faz o scan exato de backlog somente após um sweep sem mutação, remove payload/secret/metadata/comandos/auditoria/buckets e publica idade atual em log estruturado. Purge de workspace usa checkpoint, lease, fencing, serialização com rotação e rewind de dependências tardias. A quarentena persistente de restore só libera readiness após reconciliação segura.
