# ADR-010: Tenancy, API keys e quotas persistentes

## Status: Accepted

## Contexto

O serviço é multi-tenant, não possui login humano e precisa limitar abuso sem Redis. Um ID informado pelo cliente não pode selecionar tenant. Reinícios não devem zerar quotas documentadas.

## Decisão

Derivar `workspace_id` somente da API key. Toda query/transição pública combina resource ID e workspace. Tokens têm 256 bits, são enviados apenas em `Authorization`, armazenados como prefixo + verificador HMAC-SHA256 com pepper externo e comparados constant-time; não há cache de auth no MVP. Quotas autenticadas e replays usam buckets/transações PostgreSQL; limiter bounded em memória filtra brute force por instância. Fan-out máximo 100, paginação máxima 100 e quotas iniciais ficam no documento de arquitetura.

## Alternativas descartadas

- Workspace em header/body: permite confusão de autoridade.
- Hash rápido sem pepper/cache de auth: aumenta impacto de dump ou atrasa revogação.
- Limites apenas em memória: reinício/réplicas burlam quotas prometidas.

## Consequências

Autenticação consulta banco em cada request no MVP. Quotas globais geram escrita controlada e devem ser benchmarkadas. Falha de recurso de outro tenant é indistinguível de inexistente e testes negativos cobrem todas as rotas.

