# Security Audit — Deploy EasyPanel — 2026-09-25

## Escopo

Diferença entre `v1.0.1` e o candidato de demonstração pública: configuração por
socket PostgreSQL, Compose produtivo, gerador/montagem de secrets, landing page,
Swagger UI, endpoint de descoberta e pipeline de CI.

## Dependências

- Status local: ✅ limpo.
- `npm audit --production`: não aplicável; o repositório não possui manifest ou
  runtime Node.js.
- `govulncheck ./...`: nenhuma vulnerabilidade Go alcançável.
- Swagger UI `v5.33.0` é copiado como assets estáticos de uma imagem pinada por digest;
  Nginx/Node da imagem de origem não chegam ao runtime.
- Gate de imagens: ✅ SBOMs CycloneDX/SPDX gerados e Trivy sem achados
  High/Critical nas imagens `showcase`/`swagger`, em daemon limpo.

## Secrets Scan

- Status: ✅ nenhum secret exposto.
- Gitleaks examinou o histórico Git completo do candidato e o snapshot atual de arquivos
  versionados + untracked não ignorados.
- `.env`, `.env.*`, certificados, chaves e `secrets/` permanecem ignorados.
- O gerador usa CSPRNG do OpenSSL, materiais independentes, `umask 077`, arquivo
  `0600` e recusa sobrescrita.
- Um job sem rede, limitado a `CAP_CHOWN`/`CAP_FOWNER`, materializa os secrets como
  arquivos `0400` no UID específico; runtimes montam o volume em read-only. Senhas
  não aparecem nos DSNs, imagens ou argumentos de build.

## Headers HTTP

- Status: ✅ configurados e cobertos por teste.
- API produtiva, showcase e docs emitem CSP, HSTS, `X-Frame-Options: DENY`,
  `X-Content-Type-Options: nosniff` e `Referrer-Policy: no-referrer`.
- Swagger usa servidor Go próprio, rootfs read-only e CSP restrita a assets locais e
  ao origin HTTPS exato da API. Autorização não é persistida e submit público fica
  desabilitado.

## OWASP Top 10

- A01 Broken Access Control: ✅ rotas `/v1/*` continuam autenticadas/tenant-scoped;
  showcase/docs não acessam banco; roles PostgreSQL continuam mínimas.
- A02 Cryptographic Failures: ✅ TLS termina no ingress; TCP do PostgreSQL está
  desabilitado no host único; socket local usa SCRAM e key material distinto.
- A03 Injection: ✅ nenhum SQL dinâmico novo; valores de role entram por variáveis
  `psql` e senhas geradas aceitam somente base64url. URLs renderizadas usam
  `html/template`; origin CSP possui validação estrita.
- A04 Insecure Design: ✅ migration é job explícito; API/worker esperam seu sucesso;
  demo usa somente dados sintéticos e não oferece execução anônima pelo Swagger.
- A05 Security Misconfiguration: ✅ profile `production`, HTTP de destino desativado,
  profiling ausente, nenhuma porta publicada, banco com `network_mode: none`,
  runtimes non-root/read-only/cap-drop/no-new-privileges e recursos limitados.
- A06 Vulnerable Components: ✅ código Go sem vulnerabilidade alcançável e imagens
  novas aprovadas no gate Trivy High/Critical.
- A07 Authentication Failures: ✅ root público expõe apenas quatro campos fixos;
  rotas de negócio mantêm API key, scopes, quotas e revogação existentes.
- A08 Software and Data Integrity: ✅ imagens-base/actions pinadas por SHA/digest,
  OpenAPI validado, SBOMs publicados e topologia produtiva exercitada a partir do
  Compose versionado.
- A09 Security Logging and Monitoring Failures: ✅ novos servidores não recebem
  secrets/dados e não logam query, header ou body; probes de API/worker não são
  publicadas.
- A10 SSRF: ✅ política de egress do worker não mudou; produção continua aceitando
  somente HTTPS validado/fixado. Showcase/docs não realizam fetch server-side.

## Resultado final

✅ **APROVADO — apto para deploy**. O job `easypanel-production-smoke` da
[CI #36125885550](https://github.com/gabrieldosprazeres/go-webhook-delivery/actions/runs/36125885550)
comprovou em daemon limpo: build completo, PostgreSQL/SCRAM por socket, migrations,
ownership `0400:65532:65532`, readiness, ausência de porta do banco, runtimes
non-root/read-only, SBOMs e zero High/Critical nas duas imagens públicas. Testes,
race detector, análise estática, `govulncheck`, OpenAPI e Gitleaks também passaram.

O Docker local não executou esse gate por corrupção preexistente do content store
(`input/output error` ao ler o blob pinado do PostgreSQL). Nenhum prune ou remoção foi
feito, preservando os ambientes dos demais projetos; a CI em daemon limpo forneceu a
evidência final reproduzível.
