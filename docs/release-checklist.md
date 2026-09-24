# Checklist de release

`v1.0.0` só pode ser criada depois de todos os itens abaixo. A implementação da Sprint
6, por si só, não autoriza commit, push ou tag.

## Código e segurança

- [ ] Code Review independente aprovado sem blocker/warning.
- [ ] QA independente aprovado e relatório versionado.
- [ ] Security review final de DDL, funções privilegiadas, containers e release.
- [ ] Matriz adversarial completa sem skip; race, stress e leaks verdes.
- [ ] Migrations 1–20 validadas em `up/down/up`, boundaries e ACL/RLS.
- [ ] `make rollback-rehearsal` prova guard fail-closed com dados v5 e recovery forward.
- [ ] `make check`, `make integration`, `make quickstart` e `make adversarial` verdes.
- [ ] Scanner de segredo sem finding e dependências Go sem vulnerabilidade alcançável.

## Artefatos

- [ ] Build limpo com `WDE_VERSION` candidato e `WDE_REVISION` igual ao commit final.
- [ ] Smoke das quatro imagens exatas aprovado.
- [ ] Manifesto de cada imagem prova base intacta por hash/tipo/modo/UID/GID/link e
  somente as adições explicitamente permitidas.
- [ ] CycloneDX e SPDX gerados para cada digest exato.
- [ ] Trivy sem High/Critical; qualquer exceção válida está versionada e não expirou.
- [ ] Labels OCI, usuário non-root, rootfs read-only, cap-drop e healthchecks conferidos.
- [ ] Evidência contém digests, `tools.tsv`, metadata da base Trivy e checksums, sem segredo.

## Produto e operação

- [ ] OpenAPI, README, quickstart, runbook, benchmark e riscos residuais revisados.
- [ ] Runbooks de revogação, rotação, SSRF, restore quarantine e rollback ensaiados.
- [ ] Benchmark publicado com ambiente/dataset e divergência de delivery aceita.
- [ ] `CHANGELOG.md` final gerado somente após QA aprovar.
- [ ] Working tree e release candidate correspondem; nenhum artefato temporário resta.

## Publicação

- [ ] Criar commit de release revisado.
- [ ] Criar tag anotada `v1.0.0` no commit aprovado e assiná-la quando disponível.
- [ ] Publicar imagens pelos digests já escaneados, nunca apenas por tag mutável.
- [ ] Anexar SBOMs/changelog, validar quickstart publicado e observar rollout.

Se qualquer digest mudar, volte ao build, smoke, SBOM e scan. Se um downgrade puder
perder dados, interrompa a release e use correção forward-only conforme o runbook.
