# Deploy da demonstração no EasyPanel

Este runbook publica o case em uma VPS de host único. Ele não transforma a demo em
SaaS, HA ou ambiente apto a dados reais.

## Topologia

```text
Internet
   |
   +-- HTTPS --> showcase:8080  (landing page para RH)
   +-- HTTPS --> swagger:8080   (OpenAPI navegável)
   +-- HTTPS --> api:8080       (API autenticada)

worker ------------------------------> destinos HTTPS públicos
  |                 volume de socket Unix
api + migrate -----------------------> PostgreSQL 17

sem domínio/porta: secrets-init, postgres, migrate, worker, api:9090 e worker:9091
```

O arquivo implantado é [`compose.easypanel.yaml`](../compose.easypanel.yaml), separado
do Compose local. A landing page não acessa banco, API interna ou secrets.

## 1. Pré-deploy reproduzível

Em um checkout limpo do commit candidato:

```bash
make check
make easypanel-smoke
```

O smoke gera credenciais descartáveis, sobe a topologia completa, verifica
readiness, Swagger, landing page, ausência de porta do PostgreSQL e hardening dos
containers, e remove volumes/segredos ao sair.

## 2. Gerar os segredos uma única vez

```bash
./scripts/generate-easypanel-secrets.sh ./easypanel.production.env
```

O script recusa sobrescrita e cria o arquivo como `0600`. Ele contém cinco
credenciais PostgreSQL, cinco peppers independentes e dois keyrings independentes.
Nunca cole esse arquivo em issue, commit, log, chat, screenshot ou documentação.

Depois de cadastrar os valores no EasyPanel, mantenha uma cópia cifrada fora da VPS
ou remova o arquivo local por um meio recuperável. Perder keyrings torna payloads e
segredos armazenados irrecuperáveis; rotacioná-los exige o procedimento do runbook.

## 3. Criar o projeto no EasyPanel

1. Entrar no painel da VPS correta e criar o projeto `webhook-delivery-engine`.
2. Adicionar um serviço **Compose** com fonte Git.
3. Usar `https://github.com/gabrieldosprazeres/go-webhook-delivery`, branch `main` e
   arquivo `compose.easypanel.yaml`.
4. Colar o conteúdo de `easypanel.production.env` no editor de Environment.
5. Acrescentar as variáveis não secretas abaixo com as URLs definitivas:

```dotenv
WDE_VERSION=v1.1.0
WDE_REVISION=<commit-publicado>
WDE_IMAGE_TAG=v1.1.0
WDE_SHOWCASE_API_URL=https://api.<seu-dominio>
WDE_SHOWCASE_DOCS_URL=https://docs.<seu-dominio>
WDE_DOCS_API_URL=https://api.<seu-dominio>
WDE_SHOWCASE_GITHUB_URL=https://github.com/gabrieldosprazeres/go-webhook-delivery
WDE_SHOWCASE_RELEASE_URL=https://github.com/gabrieldosprazeres/go-webhook-delivery/releases
WDE_SHOWCASE_LINKEDIN_URL=https://www.linkedin.com/in/gabrieldosprazeres
```

Não crie senha manual fraca e não reutilize os valores entre variáveis.

## 4. Domínios e TLS

No EasyPanel, cadastre exatamente:

| Superfície | Serviço | Porta interna | Exposição |
|---|---:|---:|---|
| Página do case | `showcase` | `8080` | pública, HTTPS |
| Swagger | `swagger` | `8080` | pública, HTTPS |
| API | `api` | `8080` | pública, HTTPS |

Não atribua domínio a `postgres`, `migrate`, `worker`, porta `9090` da API ou porta
`9091` do worker. Não adicione `ports:` ao Compose produtivo. Ative certificado TLS
para os três hosts públicos antes de divulgar as URLs.

## 5. Primeiro deploy

O EasyPanel executa `docker compose up --build -d`. A ordem declarada é:

1. `secrets-init`, sem rede, materializa secrets `0400` para os runtimes e encerra;
2. PostgreSQL inicializa o volume, SCRAM e roles mínimas;
3. `migrate` espera ambos, aplica as migrations e encerra com sucesso;
4. API e worker iniciam somente após a migration;
5. showcase e Swagger ficam independentes do banco.

O primeiro deploy precisa terminar com PostgreSQL/API/worker/showcase/Swagger ativos e
`secrets-init`/`migrate` concluídos com exit code `0`. Jobs one-shot parados com
sucesso não são falha.

## 6. Verificação externa

```bash
curl --fail --head https://<pagina-do-case>
curl --fail --head https://docs.<seu-dominio>
test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  https://api.<seu-dominio>/livez)" = "404"
```

Abra a landing page em janela anônima, siga o link do Swagger e confirme que nenhum
token, payload ou credencial aparece. A API deve exigir `Authorization` nas rotas
`/v1/*`; `GET /` expõe somente metadados fixos de descoberta e probes operacionais
continuam inacessíveis externamente. O Swagger público é deliberadamente read-only:
ele documenta a API real sem armazenar autorização ou oferecer execução anônima.

## 7. Backup, atualização e rollback

- Faça snapshot/backup cifrado do volume `postgres-data` fora da VPS e ensaie restore.
- Antes de restaurar, mantenha API/worker fora do tráfego e execute a quarentena do
  runbook operacional.
- Para atualizar, fixe `WDE_VERSION`, `WDE_IMAGE_TAG` e `WDE_REVISION` ao release
  aprovado; não implante commit desconhecido.
- Migrations são forward-first. Se o novo runtime falhar depois de migration
  incompatível, corrija para frente; não execute downgrade destrutivo improvisado.
- Reverter somente o commit é permitido quando a migration permanece compatível e o
  checklist do release confirma isso.

## Limites declarados

- uma VPS, um PostgreSQL, sem failover ou multi-região;
- dados exclusivamente sintéticos;
- secrets do EasyPanel não substituem KMS/HSM;
- backup e restore dependem da operação da VPS;
- a superfície pública deve receber rate limiting/WAF do provedor se a demo passar a
  atrair tráfego não controlado.

Referências operacionais: [Compose Service do EasyPanel](https://easypanel.io/docs/services/compose),
[serviço PostgreSQL do EasyPanel](https://easypanel.io/docs/services/postgres) e
[secrets do Docker Compose](https://docs.docker.com/reference/compose-file/secrets/).
