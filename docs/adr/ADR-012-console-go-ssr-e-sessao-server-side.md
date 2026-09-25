# ADR-012: Console Go SSR com sessão server-side

## Status: Accepted

## Contexto

A API-first prova o motor, mas não oferece uma jornada demonstrável para cadastrar um destino, publicar um evento e acompanhar tentativas. A API pública aceita somente API key Bearer e não deve passar a aceitar cookies.

## Decisão

Adicionar `cmd/console` como BFF/SSR separado, no mesmo monólito modular. HTML e mutações usam a mesma origem. A API key é apresentada apenas no login e trocada por uma sessão aleatória de 256 bits. O banco persiste somente prefixo e HMAC do token. O cookie é `__Host-wde_session`, `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/` e sem `Domain`.

A sessão expira após 15 minutos inativa ou 60 minutos absolutos. Revogação/expiração da API key, suspensão do workspace e restore invalidam a sessão. Scopes são relidos a cada retomada. Toda mutação exige `Origin` exata, JSON ou form allowlisted e token CSRF assinado, vinculado à sessão.

O console reutiliza serviços de domínio; não chama a API por loopback e não faz fetch direto ao destino.

## Alternativas descartadas

- Guardar a API key no navegador ou sessão: amplia impacto de XSS e vazamento.
- Fazer a API aceitar cookie: mistura contratos e introduz CSRF na superfície pública.
- SPA/JWT: adiciona dependências e estado cliente sem necessidade para a demonstração.

## Consequências

Surge um quarto runtime e uma role PostgreSQL própria. CSP, CSRF, sessão, RLS e browser smoke tornam-se gates de release. O ADR-001 permanece válido como monólito modular, mas a lista de binários produtivos passa a incluir o console.
