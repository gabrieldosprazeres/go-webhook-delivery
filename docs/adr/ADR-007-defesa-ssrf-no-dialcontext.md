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

Há falsos positivos possíveis e as allowlists baseadas nos registros IANA especial e de unicast alocado precisam manutenção. Todo prefixo não listado de `2000::/3`, inclusive os blocos reservados acima de `2c00::/12`, falha fechado. Testes cobrem os registros IPv4/IPv6, NAT64/6to4, mapped IP, IDN, DNS misto, rebinding, SNI sobre IP fixado, headers excessivos, redirect, proxy, TLS e deadlines multi-address. Egress firewall é defesa adicional recomendada.

## Implementação

Materializada na Sprint 4 no pacote `internal/outboundhttp`: parser IDNA estrito, classificação fail-closed baseada no registro IANA, IPv6 restrito ao espaço global alocado, validação do conjunto completo de respostas DNS, endereço IP fixado no dial e transporte com proxy/redirect desabilitados, TLS verificado e limites de conexão, headers, corpo e tempo.
