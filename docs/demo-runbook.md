# Demonstração de portfólio

Objetivo: em até dez minutos, demonstrar confiabilidade, segurança e operação sem usar
dados reais ou exibir credenciais.

## Jornada automatizada

```bash
make quickstart
```

O script cria PostgreSQL 17 efêmero, migra o schema, inicia API/worker/Chaos Lab,
protege a credencial sintética em arquivo `0600` e demonstra:

1. bootstrap, endpoint e evento idempotente;
2. assinatura HMAC, rotação e assinatura dupla;
3. falhas transitórias, `Retry-After`, timeout e sucesso após retry;
4. falha permanente, DLQ e replay;
5. repetição do mesmo replay com a mesma chave/corpo retornando o mesmo comando/run sem
   mutação adicional;
6. métricas internas e ausência das probes na superfície pública.

O cleanup remove processos, credencial, banco e diretório temporário mesmo em erro.
Não copie a saída de debug para a apresentação.

## Restart e rastreamento

Para a demonstração visual de restart, use apenas ambiente local descartável:

```bash
make container-smoke
```

O smoke prova health/readiness e hardening do container. A suíte de integração cobre
crash, lease expirado, fencing e retomada; não mate um worker durante a quickstart, pois
isso transformaria uma demo determinística em teste manual não reproduzível.

Para traces, configure um coletor OTLP/HTTP local e mantenha a porta privada. O mesmo
trace correlaciona request, persistência, claim e finalização por IDs opacos. Mostre
nomes/status bounded; nunca payload, tenant, URL, header, segredo ou ciphertext.

## Mensagem de portfólio

O ponto central não é “mais um CRUD”: o engine torna entrega externa não confiável em
um workflow durável com PostgreSQL, idempotência, at-least-once, leases/fencing,
retry/DLQ, isolamento tenant, HMAC, anti-SSRF, criptografia, retenção e observabilidade
segura. Limitações honestas: sem exactly-once, HA/multi-região, KMS/HSM ou garantia de
100 deliveries/s no benchmark local atual.
