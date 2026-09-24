# Checklist de release

`v1.0.0` só pode ser criada depois de todos os itens abaixo. A implementação da Sprint
6, por si só, não autoriza commit, push ou tag.

## Código e segurança

- [x] Code Review independente aprovado sem blocker/warning.
- [x] QA independente aprovado e relatório versionado.
- [x] Security review final de DDL, funções privilegiadas, containers e release.
- [x] Matriz adversarial completa sem skip; race, stress e leaks verdes.
- [x] Migrations 1–20 validadas em `up/down/up`, boundaries e ACL/RLS.
- [x] `make rollback-rehearsal` prova guard fail-closed com dados v5 e recovery forward.
- [x] `make check`, `make integration`, `make quickstart` e `make adversarial` verdes.
- [x] Scanner de segredo sem finding e dependências Go sem vulnerabilidade alcançável.

## Artefatos

- [x] Build limpo com `WDE_VERSION` candidato e `WDE_REVISION` igual ao commit final.
- [x] Smoke das quatro imagens exatas aprovado.
- [x] Manifesto de cada imagem prova base intacta por hash/tipo/modo/UID/GID/link e
  somente as adições explicitamente permitidas.
- [x] CycloneDX e SPDX gerados para cada digest exato.
- [x] Trivy sem High/Critical; qualquer exceção válida está versionada e não expirou.
- [x] Labels OCI, usuário non-root, rootfs read-only, cap-drop e healthchecks conferidos.
- [x] Evidência contém digests, `tools.tsv`, metadata da base Trivy e checksums, sem segredo.

## Produto e operação

- [x] OpenAPI, README, quickstart, runbook, benchmark e riscos residuais revisados.
- [x] Runbooks de revogação, rotação, SSRF, restore quarantine e rollback ensaiados.
- [x] Benchmark publicado com ambiente/dataset e divergência de delivery aceita.
- [x] `CHANGELOG.md` final gerado somente após QA aprovar.
- [x] Working tree e release candidate correspondem; nenhum artefato temporário resta.

## Publicação

- [x] Criar commit de release revisado.
- [x] Criar tag anotada `v1.0.0` no commit aprovado; assinatura não foi exigida para a
  release local.
- [ ] Publicar imagens pelos digests já escaneados, nunca apenas por tag mutável — fora
  do escopo local e sem registry configurado.
- [ ] Anexar SBOMs/changelog, validar quickstart publicado e observar rollout — depende
  de um destino remoto, inexistente neste checkout.

Se qualquer digest mudar, volte ao build, smoke, SBOM e scan. Se um downgrade puder
perder dados, interrompa a release e use correção forward-only conforme o runbook.
