# ADR-005: Stack HTTP, persistência, migrações e ferramentas

## Status: Accepted

## Contexto

O projeto deve demonstrar Go idiomático e manter controle de HTTP, SQL e concorrência, sem abstrações de framework escondendo propriedades críticas.

## Decisão

Usar Go 1.27.x pinado, `net/http`/`ServeMux`, `encoding/json`, `log/slog`, `pgx/v5`, `sqlc`, migrations Goose versionadas, OpenTelemetry/Prometheus e Testcontainers. Ferramentas ficam pinadas no `go.mod` com `go get -tool`. SQL é explícito; interfaces pequenas ficam no consumidor; DI é manual nos `main`.

## Alternativas descartadas

- Gin/Fiber: sem necessidade para a superfície proposta.
- ORM/repository genérico: ocultaria SQL, locks e constraints importantes.
- Framework de DI/service locator: reduz rastreabilidade de dependências.

## Consequências

O código tem menos magia e mais SQL revisável. Há mapeamento explícito entre domínio e `sqlc`. Atualizações de toolchain/dependências são controladas e verificadas por CI.

