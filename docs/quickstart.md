# Quickstart e demonstração

## Jornada automatizada

Pré-requisitos: Go 1.27.1, Docker com Compose, GNU Make, `curl`, `sed` e `grep`.

```bash
make quickstart
```

O comando usa um projeto Compose e volume PostgreSQL exclusivos, portas loopback alternativas e um diretório temporário `0700`. Ele executa migrations, inicia API/worker/Chaos Lab, cria a credencial em arquivo `0600` e comprova:

1. entrega HMAC válida;
2. rotação com duas assinaturas e replay idempotente repetido com o mesmo comando/run;
3. duas respostas `503` antes do sucesso;
4. timeouts até `dead_letter`;
5. cenários `429 Retry-After` e falha permanente;
6. métricas internas de API e worker.

Segredos e payloads não são impressos. Processos, arquivos e volume efêmeros são removidos mesmo em falha. A demo usa somente dados sintéticos e normalmente termina em menos de um minuto; dez minutos é o orçamento documentado para máquina fria.

## Fluxo manual com curl

Depois de subir o profile `core` e criar `local-credentials.json` conforme o README, crie uma configuração local do curl sem colocar a API key na URL ou no histórico:

```bash
umask 077
API_KEY=$(sed -n 's/.*"api_key": "\([^"]*\)".*/\1/p' local-credentials.json)
printf 'header = "Authorization: Bearer %s"\nheader = "Content-Type: application/json"\n' "$API_KEY" > local-curl.conf
chmod 600 local-curl.conf
unset API_KEY
```

Cadastre um receptor HTTPS real ou execute API, worker e Chaos Lab localmente como o script automatizado. A política SSRF bloqueia corretamente IPs privados da bridge Docker, por isso o container do Chaos Lab não é um atalho para egress entre containers.

```bash
curl --config local-curl.conf --fail --data-binary \
  '{"url":"https://receiver.example/webhooks","event_types":["invoice.created"]}' \
  http://127.0.0.1:8080/v1/endpoints

curl --config local-curl.conf --fail \
  -H 'Idempotency-Key: event-demo-001' \
  --data-binary '{"type":"invoice.created","data":{"invoice_id":"inv_demo_001","amount":1999}}' \
  http://127.0.0.1:8080/v1/events
```

Copie o `delivery_id` devolvido para uma variável e acompanhe a timeline; aceite `202` significa persistência, não entrega concluída.

```bash
DELIVERY_ID='<uuid-sintetico-retornado>'
curl --config local-curl.conf --fail \
  "http://127.0.0.1:8080/v1/deliveries/$DELIVERY_ID"

curl --config local-curl.conf --fail \
  -H 'Idempotency-Key: replay-demo-001' \
  --data-binary '{"reason":"receiver recovery verified"}' \
  "http://127.0.0.1:8080/v1/deliveries/$DELIVERY_ID/replays"
```

Para rotacionar o segredo, use o `endpoint.id` da criação. A resposta revela o novo segredo uma vez; o consumidor deve aceitar ambas as assinaturas durante o overlap.

```bash
ENDPOINT_ID='<uuid-sintetico-retornado>'
curl --config local-curl.conf --fail \
  -H 'Idempotency-Key: rotation-demo-001' \
  --data-binary '{"overlap_seconds":3600}' \
  "http://127.0.0.1:8080/v1/endpoints/$ENDPOINT_ID/secret-rotations"
```

As superfícies operacionais não compartilham a porta pública:

```bash
curl --fail http://127.0.0.1:9090/livez
curl --fail http://127.0.0.1:9090/readyz
curl --fail http://127.0.0.1:9090/metrics
curl --fail http://127.0.0.1:9091/metrics
```

Os exemplos usam loopback. Qualquer bind operacional não-loopback exige rede privada e ACL/NetworkPolicy que negue ingress público, pois probes e métricas não possuem autenticação de aplicação.

Apague os arquivos locais quando terminar:

```bash
rm local-curl.conf local-credentials.json
```

O contrato completo e exemplos sintéticos ficam em [`../api/openapi.yaml`](../api/openapi.yaml). O consumidor HMAC de referência fica em [`../examples/hmac-consumer`](../examples/hmac-consumer).
