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
   +-- HTTPS --> console:8082   (painel autenticado)

operador -- túnel SSH --> 127.0.0.1:33000 --> grafana:3000

worker ------------------------------> destinos HTTPS públicos
  |                 volume de socket Unix
api + console + migrate -------------> PostgreSQL 17

rede privada: OTel Collector, Prometheus, Tempo e listeners 9090/9091/9092
bridge dedicada sem pares: Grafana --> bind 127.0.0.1:33000
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

O script recusa sobrescrita e cria o arquivo como `0600`. Ele contém seis
credenciais PostgreSQL, sete peppers independentes, dois keyrings independentes e a
senha administrativa aleatória do Grafana.
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
WDE_VERSION=<release-aprovada>
WDE_REVISION=<commit-publicado>
WDE_IMAGE_TAG=<release-aprovada>
WDE_SHOWCASE_API_URL=https://api.webhooks.gabrieldosprazeres.com.br
WDE_SHOWCASE_DOCS_URL=https://docs.webhooks.gabrieldosprazeres.com.br
WDE_SHOWCASE_CONSOLE_URL=https://console.webhooks.gabrieldosprazeres.com.br
WDE_DOCS_API_URL=https://api.webhooks.gabrieldosprazeres.com.br
WDE_CONSOLE_ORIGIN=https://console.webhooks.gabrieldosprazeres.com.br
WDE_GRAFANA_LOCAL_PORT=33000
WDE_GRAFANA_ROOT_URL=http://localhost:33000
WDE_SHOWCASE_GITHUB_URL=https://github.com/gabrieldosprazeres/go-webhook-delivery
WDE_SHOWCASE_RELEASE_URL=https://github.com/gabrieldosprazeres/go-webhook-delivery/releases
WDE_SHOWCASE_LINKEDIN_URL=https://www.linkedin.com/in/gabrieldosprazeres
```

Não crie senha manual fraca e não reutilize os valores entre variáveis.

## 4. Domínios e TLS

No EasyPanel, cadastre exatamente:

| Superfície | Hostname | Serviço | Porta interna | Exposição |
|---|---|---:|---:|---|
| Página do case | `webhooks.gabrieldosprazeres.com.br` | `showcase` | `8080` | pública, HTTPS |
| Swagger | `docs.webhooks.gabrieldosprazeres.com.br` | `swagger` | `8080` | pública, HTTPS |
| API | `api.webhooks.gabrieldosprazeres.com.br` | `api` | `8080` | pública, HTTPS |
| Console | `console.webhooks.gabrieldosprazeres.com.br` | `console` | `8082` | autenticada, HTTPS |

Não atribua domínio a `postgres`, `migrate`, `worker`, `otel-collector`, `prometheus`,
`tempo`, `grafana` ou às portas `9090`–`9092`. O único bind da stack operacional é o
Grafana em `127.0.0.1:33000`; loopback não aceita tráfego externo e não deve ser alterado
para `0.0.0.0`. Ele não faz parte da demo para recrutadores.

Para que o Docker materialize esse bind, somente o Grafana participa também da bridge
`grafana-host-access`, que não possui outros serviços. Pré-instalação e auto-update de
plugins ficam desabilitadas. O operador acessa o Grafana somente pelo túnel SSH
criptografado:

```bash
ssh -N -L 33000:127.0.0.1:33000 <usuario-vps>@2.24.90.172
```

Com o túnel ativo, abra `http://localhost:33000` e autentique como `operator`. O cookie
não usa `Secure` porque termina em HTTP no loopback do navegador; o transporte entre a
máquina do operador e a VPS é o canal SSH. `SameSite=Strict`, autenticação obrigatória
e ausência de signup/anônimo continuam aplicados.

## 5. Primeiro deploy

O EasyPanel executa `docker compose up --build -d`. A ordem declarada é:

1. `secrets-init`, sem rede, materializa secrets `0400` para os runtimes e encerra;
2. PostgreSQL inicializa o volume, SCRAM e roles mínimas;
3. `roles-init` cria/atualiza de forma idempotente as roles do console em volumes já existentes;
4. `migrate` espera os jobs, aplica as migrations e encerra com sucesso;
5. API, worker e console iniciam somente após a migration;
6. Collector, Prometheus, Tempo e Grafana formam a stack operacional privada;
7. showcase e Swagger ficam independentes do banco.

O primeiro deploy precisa terminar com PostgreSQL/API/worker/console/stack de
observabilidade/showcase/Swagger ativos e `secrets-init`/`roles-init`/`migrate`
concluídos com exit code `0`. Jobs one-shot parados com
sucesso não são falha.

## 6. Verificação externa

```bash
curl --fail --head https://webhooks.gabrieldosprazeres.com.br
curl --fail --head https://docs.webhooks.gabrieldosprazeres.com.br
curl --fail --head https://console.webhooks.gabrieldosprazeres.com.br/login
test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  https://api.webhooks.gabrieldosprazeres.com.br/livez)" = "404"
```

Abra a landing page em janela anônima, siga o link do Swagger e confirme que nenhum
token, payload ou credencial aparece. A API deve exigir `Authorization` nas rotas
`/v1/*`; `GET /` expõe somente metadados fixos de descoberta e probes operacionais
continuam inacessíveis externamente. O Swagger público é deliberadamente read-only:
ele documenta a API real sem armazenar autorização ou oferecer execução anônima.

No console, use uma API key criada pelo bootstrap. A chave é trocada por sessão curta
e não é persistida. Cadastre um endpoint HTTPS sintético, publique um evento, acompanhe
a delivery e confira o painel de 24 horas. Para uma entrevista, compartilhe a API key
de demo por canal privado e revogue-a depois; nunca publique a chave no GitHub ou na
landing page. Para a observabilidade operacional, abra primeiro o túnel SSH descrito
acima e entre em `http://localhost:33000` com `operator` (ou
`WDE_GRAFANA_ADMIN_USER`) e a senha gerada em `WDE_GRAFANA_ADMIN_PASSWORD`.
Prometheus, Tempo e Collector não possuem interface externa; são consumidos pelo
Grafana dentro da rede privada.

Na verificação interna, confirme ainda que o Tempo usa `/var/tempo` como `tmpfs` de
256 MiB, não como volume nomeado, e que a regra Prometheus
`WDETempoDiscardingSpans` está carregada. Traces são evidência operacional efêmera:
reiniciar o Tempo os remove, enquanto API, worker e console continuam funcionais.

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
