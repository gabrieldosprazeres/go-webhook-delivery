# ADR-007: Cliente HTTP com defesa SSRF no DialContext

## Status: Accepted

## Contexto

URLs e DNS são controlados por usuário. Validar no cadastro e depois deixar o transporte resolver novamente permite rebinding e acesso a redes internas.

## Decisão

Aceitar HTTPS absoluto, canonicalizar host e rejeitar formas ambíguas. Em cada tentativa, resolver A/AAAA com timeout, falhar se qualquer IP for proibido e conectar somente a um IP validado via `DialContext`, preservando hostname para SNI/certificado. Proxy ambiental e redirects ficam desabilitados. TLS não admite skip-verify. DNS, connect, handshake, headers, total, resposta e goroutines têm limites.

## Alternativas descartadas

- Validar apenas no cadastro: DNS pode mudar.
- Validar e rediscutir hostname no transporte: cria TOCTOU.
- Allowlist global de domínios: incompatível com destinos arbitrários do produto.

## Consequências

Há falsos positivos possíveis e política de faixas precisa manutenção. Testes cobrem IPv4/IPv6, mapped IP, IDN, rebinding, redirect, proxy e TLS. Egress firewall é defesa adicional recomendada.

