# QA Report: Sprint 6 — Hardening e release de portfólio

## Status: ✅ Aprovado

**Data:** 2026-09-24
**Branch:** `feature/sprint-6-hardening-release`
**Escopo:** validação independente local, PostgreSQL 17.11 nativo e ambiente Docker
isolado `colima-wde-release`, preservando os 13 containers originais.

O código, os contratos que independem de Docker e a matriz completa contra PostgreSQL
17.11 nativo foram aprovados sem falhas. O rebuild, smoke real, SBOMs e scanners também
passaram no ambiente Docker isolado. A Sprint 6 satisfaz seus critérios de QA; security
audit, changelog e criação do commit/tag `v1.0.0` permanecem como etapas pós-QA do
pipeline de release.

## Validação estática e build

| Check | Resultado |
|---|---|
| `make check` | ✅ Todos os alvos concluídos |
| `gofmt` / `go mod tidy -diff` | ✅ |
| `go vet ./...` | ✅ |
| Staticcheck | ✅ |
| `govulncheck ./...` | ✅ Nenhuma vulnerabilidade alcançável |
| OpenAPI | ✅ |
| Goose `validate` | ✅ |
| Build de `api`, `worker` e `chaoslab` | ✅ |
| `git diff --check` | ✅ |
| `sh -n scripts/*.sh` | ✅ 10 scripts válidos |
| `make secret-scan` | ✅ Gitleaks: 6 commits, ~1,13 MB, zero leaks |
| Rebuild das quatro imagens | ✅ API, worker, Chaos Lab e migrator |
| `make container-smoke` | ✅ Smoke real completo no `colima-wde-release` |
| SBOM | ✅ CycloneDX e SPDX para as quatro imagens |
| Trivy | ✅ 0 High/Critical nas quatro imagens |

## Testes executados

| Suíte | Categoria | Passaram | Falharam | Skip |
|---|---|---:|---:|---:|
| `go test -json -count=1 ./...` | Regressão sem cache | 207 | 0 | 43 |
| `go test -race -json -count=1 ./...` | Regressão sem cache + race | 207 | 0 | 43 |
| `make adversarial` em PostgreSQL 17.11 nativo | Árvore completa serial | PASS integral | 0 | 0 |
| `make integration` em PostgreSQL 17.11 temporário | Integração S1–S6 | PASS integral | 0 | 0 |
| `DOCKER_CONTEXT=colima-wde-release make rollback-rehearsal` | Rollback real | 7 | 0 | 0 |
| `go test -count=100 ./test/imagelayout ./test/benchmark` | Stress focado | 2.500 | 0 | 0 |
| `go test -race -count=20 ./test/imagelayout ./test/benchmark` | Stress focado + race | 500 | 0 | 0 |
| `./scripts/container-smoke.sh --layout-contract` | Contrato local, sem Docker | 17 | 0 | 0 |
| `make container-smoke` | Container E2E real | PASS integral | 0 | 0 |
| `make quickstart` | Demonstração E2E | 7 | 0 | 0 |

Os 43 skips nas duas regressões sem DSNs são exclusivamente testes que requerem as
quatro `WDE_TEST_*_DATABASE_URL`: matriz das sete rotas produtivas, `SIGTERM` do worker
contra banco e cenários em `test/integration`. A execução adversarial posterior forneceu
as quatro DSNs contra PostgreSQL 17.11 nativo, percorreu toda a árvore com exit code zero
e zero ocorrência de `--- SKIP:`. Assim, nenhum cenário permaneceu sem execução na
matriz adversarial.

O wrapper nominal `make integration` passou integralmente em PostgreSQL 17.11
temporário, incluindo as sete rotas produtivas, todos os grupos S1–S6 e o encerramento
do worker por `SIGTERM`. O wrapper literal
`DOCKER_CONTEXT=colima-wde-release make rollback-rehearsal` também passou 7/7:
boundary vazio 20→19→20, guard atômico com dado vivo, preflight externo, quarentena
global, writer concorrente e confirmação final de Goose 20/schema lógico 5/dados
intactos.

O quickstart passou 7/7, cobrindo entrega assinada, rotação dual-key, retry, DLQ,
replay e telemetria. O ambiente Docker isolado reconstruiu as quatro imagens e o smoke
real confirmou healthchecks, runtime non-root, rootfs read-only, capabilities removidas,
`no-new-privileges`, `tmpfs`, schema e contrato de filesystem. As quatro imagens geraram
SBOMs CycloneDX/SPDX e tiveram zero finding High/Critical no Trivy; Gitleaks permaneceu
com zero vazamento.

### Cobertura de risco focada

- O manifesto de imagem foi repetido contra extra, missing, alteração de conteúdo no
  mesmo path, tipo, modo, UID/GID, symlink, traversal, duplicata, ordenação e metadata
  malformada.
- O manifesto do migrator fixa o conteúdo das migrations; somente o hash dos binários
  construídos permanece wildcard intencional.
- O receptor do benchmark validou HMAC, IDs esperados distintos, duplicata que não
  mascara missing, callback tardio após shutdown/quiescência e arquivo de evidência
  privado.
- A agregação exige três rodadas completas e falha diante de duplicata, missing,
  backlog, estado final incorreto ou crescimento anômalo de RSS/heap/goroutines.

## Cobertura dos critérios da Sprint 6

| Story | Critério | Resultado |
|---|---|---|
| S6-01 | Matriz adversarial final, crash, migration e security review | ✅ Matriz completa em PG17.11, zero skip; rollback 7/7; security audit permanece etapa pós-QA |
| S6-02 | Ingestão ≥100/s, p95 <200 ms; delivery medida; soak sem crescimento contínuo | ✅ Evidência publicada; ingestão passou e delivery de 32,77/s foi documentada honestamente como divergência |
| S6-03 | Imagens mínimas/non-root/read-only, SBOM, scan e smoke | ✅ Quatro imagens reconstruídas; smoke real, CycloneDX/SPDX, Trivy 0 High/Critical e Gitleaks 0 |
| S6-04 | Runbooks, demo e limitações explícitas | ✅ Artefatos obrigatórios presentes e quickstart 7/7 |

## Documentação e operação

- `SECURITY.md`, benchmark, runbook operacional, runbook de demo, supply chain e
  checklist de release existem e cobrem API key/HMAC, SSRF, payload/keyring, banco/fila,
  dependência comprometida, restore quarantine e rollback.
- Os scripts críticos existem, têm bit executável e sintaxe POSIX válida.
- O ambiente `colima-wde-release` isolou o candidato sem interromper ou alterar os 13
  containers originais de outros projetos.
- Os IDs históricos documentados antes da correção da migration 20 permanecem stale;
  somente os artefatos reconstruídos e aprovados nesta rodada representam o candidato
  validado.

## Próximas etapas pós-QA

Nenhum critério de QA da Sprint 6 permanece pendente. O pipeline de release continua
com estas etapas, fora do parecer funcional desta sprint:

1. Security audit final de DDL/funções privilegiadas, containers e processo de release.
2. Gerar o changelog e alinhar README/status com Code Review R4 e QA aprovado.
3. Criar o commit de release e a tag anotada `v1.0.0`, preservando as evidências por
   artefato/digest validados.

## Riscos residuais

1. **Throughput de delivery abaixo do alvo.** A mediana publicada foi 32,77/s contra
   100/s. A divergência é aceita pela DoD quando documentada, mas impede promessa de
   capacidade/SLO equivalente.
2. **Identidade de release.** Os IDs anteriores à correção da migration 20 continuam
   inválidos para promoção; o pipeline deve manter a ligação entre commit, labels OCI,
   digests reconstruídos, SBOMs e scans aprovados.

## Veredicto

**QA Sprint 6 aprovado.** Foram obtidos 3.414 resultados de teste aprovados nas
execuções contadas (207 regressão, 207 regressão race, 2.500 stress e 500 stress race),
além do contrato acionado pelo smoke, da árvore adversarial integral em PostgreSQL
17.11, rollback 7/7, quickstart 7/7 e smoke real das quatro imagens; nenhuma falha foi
encontrada. Os 86 skips observados nas duas execuções sem DSNs correspondem aos mesmos
43 cenários, posteriormente executados com zero skip pela matriz adversarial, e não
foram inflados como aprovação.

As quatro imagens foram reconstruídas e produziram SBOMs CycloneDX/SPDX; Trivy retornou
zero High/Critical e Gitleaks zero vazamentos. A Sprint 6 pode avançar para o Security
Audit e, se aprovado, changelog, commit e tag de release.
