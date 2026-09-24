# Supply chain e imagens

## Artefatos

`make images` cria quatro imagens separadas: API, worker, Chaos Lab e migrator. O build
multi-stage usa Go 1.27.1 e runtime distroless pinados por digest. Os runtimes executam
como `nonroot:nonroot`, filesystem read-only, `cap_drop: ALL`,
`no-new-privileges:true` e `tmpfs` explícito. O verificador exporta base e alvo e cria
manifestos canônicos com path, tipo, modo, UID/GID, destino de link e SHA-256 de cada
arquivo regular. Todo path herdado precisa permanecer byte a byte e com metadados
idênticos. Runtime só pode acrescentar `/service`; migrator só pode acrescentar
`/goose`, o diretório `/migrations` e os SQLs cujos hashes vêm do checkout revisado.
Os hashes dos dois binários de build são a única wildcard intencional: path, tipo,
modo e owner continuam exatos, e o conteúdo fica vinculado ao digest da imagem,
SBOM e scan. Conteúdo herdado da base, inclusive um shell se outra base pinada vier a
incluí-lo, integra o manifesto explícito e não é ocultado por denylist.

Passe uma versão e revisão verificáveis ao build:

```bash
WDE_VERSION=1.0.0-rc.1 WDE_REVISION="$(git rev-parse HEAD)" make images
```

Os labels OCI `version`, `revision` e `source` permitem ligar a evidência ao código.
Nunca passe segredo como build argument ou label.

## SBOM e vulnerabilidades

O pipeline baixa ferramentas para um diretório temporário, verifica SHA-256 publicado
e remove binários/cache ao sair:

- Syft 1.52.0 para CycloneDX JSON e SPDX JSON;
- Trivy 0.74.0 para vulnerabilidades `HIGH,CRITICAL`;
- Gitleaks 8.30.1 para histórico Git.

```bash
evidence="$(mktemp -d)"
chmod 700 "$evidence"
WDE_SUPPLY_CHAIN_OUTPUT="$evidence" make supply-chain
make secret-scan
```

O scan resolve o ID imutável de cada imagem e usa esse ID tanto no SBOM quanto no
scanner. Qualquer High/Critical bloqueia; não há allowlist silenciosa. Uma exceção
futura exigiria finding, justificativa, responsável, mitigação e expiração versionados.
Os SBOMs podem conter inventário operacional e devem ser tratados como artefatos de CI,
não publicados por padrão.

O diretório de evidências também contém `tools.tsv`, com Syft, Trivy e Gitleaks,
versão, asset e checksums esperado/observado; `trivy-db-metadata.json`, copiado da base
efetivamente usada; e `gitleaks.json` redigido. Ausência desses metadados falha o gate;
assim a evidência não depende somente do log efêmero do job.

Na validação local da Sprint 6, as quatro imagens exatas apresentaram zero findings e
zero High/Critical:

| Imagem | ID local escaneado |
|---|---|
| API | `sha256:0e4026ca30336feee81b126bad37be54b12172887f48926a3d881bd546e7b59e` |
| worker | `sha256:f2d4344093dc4c264c19d4328db295d7709014bfb61f23153c63b936e03af554` |
| Chaos Lab | `sha256:13590da27dabd96ec495b8a0f5186f3850590f47bcbfc5d5f65445e0bfd52189` |
| migrator | `sha256:4da42d1e38caca0aba9b4c0ad13a98dc103447c3efcde8d23a5b315b92c0324c` |

São IDs locais do candidato de trabalho, com label da revisão base `5dcab024`; não são
digests de registry nem autorização de release. Os digests finais devem ser copiados do
`images.tsv` gerado no build do commit aprovado. Reconstruir muda o artefato e exige
novo smoke, SBOM e scan.

Após a correção R2 no conteúdo da migration 20, esses IDs são evidência histórica e
estão deliberadamente **stale** para release. O build final precisa gerar quatro novos
IDs e repetir smoke, SBOM, Trivy e Gitleaks; nenhum ID desta tabela pode ser promovido.

`make container-smoke` valida healthchecks, roles/schema, portas, usuário, rootfs,
capabilities, security options, `tmpfs` e a allowlist positiva do diff de filesystem
contra a base pinada. Conteúdo ou metadados alterados em path herdado, symlink
redirecionado, tipo/modo/owner divergente, inventário extra, incompleto, duplicado,
malformado ou com traversal falham fechados. A CI gera e anexa a evidência somente
depois de testar as mesmas imagens.
