# Security Audit — Console e Observabilidade — 2026-09-25

## Escopo

Candidato da branch `feature/console-observability`, referência de código
`072bda6`, cobrindo o console Go SSR/BFF, sessão derivada de API key, métricas do
produto, migration lógica v6, Collector OpenTelemetry, Prometheus, Tempo, Grafana,
Compose do EasyPanel, smoke de navegador e supply chain.

## Veredicto

✅ **APROVADO para pull request e pipeline de release**, sem blocker de segurança no
código. O deploy permanece condicionado ao smoke EasyPanel verde em runner Linux
limpo, SBOM e Trivy das imagens exatas, inclusive os quatro vendors de observabilidade.
Essa condição impede promover um artefato diferente do auditado. O smoke local não
iniciou por corrupção pré-existente do content store do Docker Desktop no blob do
PostgreSQL; a renderização e as invariantes estáticas do Compose foram aprovadas.

## Dependências e segredos

- `govulncheck ./...`: nenhuma vulnerabilidade Go alcançável.
- `npm audit` no pacote isolado de QA do navegador: zero vulnerabilidades. Node e
  Playwright não entram nas imagens de runtime.
- Gitleaks examinou 36 commits, o snapshot versionado e os arquivos relevantes não
  ignorados: nenhum vazamento.
- Sessão e CSRF usam peppers próprios. A API key existe somente no request de login;
  banco e cookie recebem apenas uma sessão aleatória verificada por HMAC.
- O browser smoke desliga screenshots/trace/vídeo automáticos durante o login,
  sanitiza artefatos antes de propagar o resultado e prova com canário que textos são
  redigidos e binários contaminados são removidos. Evidência só é publicada em CI
  depois de uma execução bem-sucedida.
- Em produção, peppers, keyrings, senhas PostgreSQL e credencial do Grafana são
  arquivos `0400` materializados em volume dedicado; não entram em imagem, Git ou
  variável comum do processo.

## Browser, autenticação e headers

- Cookie `__Host-wde_session`: `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`,
  sem `Domain`; idle de 15 minutos e limite absoluto de 60 minutos.
- Toda mutação exige origem exata, `Sec-Fetch-Site` aceitável, content type fechado e
  token CSRF vinculado à sessão. A API pública continua Bearer-only.
- Um guard global de borda envolve todas as rotas antes de telemetria, autenticação,
  sessão e banco; o login conserva ainda o limite específico por prefixo HMAC da key.
  A regressão prova que uma requisição limitada não incrementa o lookup de sessão.
- CSP usa somente recursos da própria origem, sem inline/eval/CDN; templates usam
  escaping contextual e o JavaScript de métricas escreve somente com `textContent`.
- HSTS no profile produtivo, `X-Frame-Options: DENY`, `nosniff`, Permissions Policy,
  `Cache-Control: no-store, private` e `Referrer-Policy: same-origin`. Esta última
  preserva o `Origin` de POSTs legítimos sem enviar referer a sites externos.
- Chromium desktop/mobile e Axe passaram: login, cookie, layout, atualização de
  métricas pausável, navegação e logout, sem violação WCAG configurada.

## OWASP Top 10

| Categoria | Resultado | Evidência principal |
|---|---|---|
| A01 — Broken Access Control | Aprovado | Workspace deriva da sessão; RLS forçada, role `wde_console` sem DML direto, scopes vivos e testes cross-tenant de leitura, replay e métricas. |
| A02 — Cryptographic Failures | Aprovado | Tokens CSPRNG, HMAC com peppers separados, comparação constante, cookies seguros e TLS público; nenhum segredo bruto persistido. |
| A03 — Injection | Aprovado | SQL parametrizado, funções `SECURITY DEFINER` com `search_path=pg_catalog`, HTML escapado e DOM atualizado com `textContent`. |
| A04 — Insecure Design | Aprovado | BFF same-origin separado, CSRF, guard global antes de tracing/sessão/banco, login limitado por prefixo, máximo de cinco sessões concorrentes, timeouts e purge bounded. |
| A05 — Security Misconfiguration | Aprovado | Containers non-root/read-only/cap-drop; Grafana somente em `127.0.0.1:33000`; Prometheus, Tempo, Collector e listeners operacionais sem ingress; Tempo em `tmpfs` limitado a 256 MiB. |
| A06 — Vulnerable Components | Aprovado | `govulncheck` e `npm audit` limpos; imagens e Actions pinadas; SBOM/Trivy obrigatórios no pipeline final. |
| A07 — Identification and Authentication Failures | Aprovado | Sessão curta server-side, revogação fail-closed, scopes relidos, expiração idle/absolute, auditoria atômica e login throttled por prefixo HMAC. |
| A08 — Software and Data Integrity Failures | Aprovado | Migrations versionadas, downgrade serializado e fail-closed, lockfiles, digests e evidência de browser sanitizada. |
| A09 — Security Logging and Monitoring Failures | Aprovado | Logs/traces allowlisted; sem payload, URL, key, cookie ou tenant em labels; ingestão Tempo limitada, regra de descarte carregada e observabilidade operacional privada. |
| A10 — SSRF | Aprovado | Console reutiliza o serviço de domínio existente; não faz fetch ao destino. HTTPS, DNS/IP público, pinagem de conexão e bloqueio de redirect continuam obrigatórios. |

## Evidências reproduzíveis

- `make check`: testes, race detector, vet, Staticcheck, `govulncheck`, OpenAPI,
  migrations e os seis builds aprovados.
- `go test ./internal/console ./internal/ratelimit -count=1`: guard pré-sessão
  aprovado, inclusive prova de que `429` não alcança o store.
- Compose de produção renderizado e validado com `tmpfs` de 256 MiB, sem volume
  `/var/tempo`, alertas read-only e zero porta pública de Tempo/Prometheus/Collector.
- `make integration` com PostgreSQL 17 e as cinco roles reais: aprovado.
- 20 logins concorrentes: exatamente cinco sessões; revogação, idle, absolute,
  restore, purge, rollback, quota e isolamento tenant aprovados.
- Consulta tenant-scoped com 25.000 tentativas: p95 observado de `19,76 ms`, abaixo
  do gate de `500 ms`.
- Playwright/Axe: duas matrizes aprovadas (`desktop-chromium` e
  `mobile-chromium`), além do canário de sanitização de artefatos.
- Revisão de código: zero blockers, warnings ou suggestions após as correções.
- QA final: aprovado sem warnings antes do endurecimento adicional dos artefatos;
  a regressão incremental correspondente também passou localmente.

## Condições operacionais

1. Não criar domínio ou bind público para Grafana, Prometheus, Tempo, Collector ou
   listeners `9090–9092`. Grafana é acessado por túnel SSH no loopback; uma bridge
   dedicada sem pares permite ao Docker materializar esse bind sem conectar o Grafana
   à rede de ingress. Pré-instalação e auto-update de plugins permanecem desabilitadas.
2. Métricas do cliente vêm do PostgreSQL com RLS; nunca adicionar `workspace_id` aos
   labels do Prometheus.
3. Não promover se o smoke EasyPanel, os targets Prometheus, o canário Tempo, os
   dashboards/datasources, o outage test, SBOM ou Trivy falharem.
4. Preservar os secrets existentes no upgrade e adicionar somente os novos materiais
   independentes do console/Grafana conforme o runbook.
5. A regra `WDETempoDiscardingSpans` é visível no Prometheus/Grafana, mas este release
   não inclui Alertmanager/contact point; o operador deve inspecioná-la durante a demo.

## Resultado final

O código está apto a seguir para CI e revisão de pull request. A aprovação para
produção torna-se definitiva somente quando o pipeline do commit final estiver verde
e o deploy mantiver a topologia privada documentada.
