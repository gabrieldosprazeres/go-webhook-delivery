# Contrato da API

O arquivo [`openapi.yaml`](openapi.yaml) contém o contrato OpenAPI 3.1 da API pública
do Webhook Delivery Engine `v1.0.0`. Ele documenta autenticação Bearer, idempotência,
endpoints, publicação de eventos, consulta de deliveries, replay, rotação de segredo,
paginação, quotas e respostas `application/problem+json`.

O contrato é carregado, tem suas referências resolvidas e é validado semanticamente
na CI. Para executar a mesma validação localmente:

```bash
make openapi-lint
```

Os exemplos usam exclusivamente dados sintéticos. Nenhuma API key ou signing secret
real deve ser adicionada ao contrato.
