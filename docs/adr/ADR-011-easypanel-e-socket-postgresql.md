# ADR-011: Deploy de demonstração no EasyPanel e socket PostgreSQL

## Status: Accepted

## Contexto

A demonstração pública roda em uma única VPS pequena gerenciada pelo EasyPanel. O
baseline produtivo exigia TLS verificado para conexão TCP ao PostgreSQL, mas publicar
ou simular TLS do banco dentro do mesmo host adicionaria certificados, rotação e uma
interface de rede sem reduzir o principal risco da demo. O banco, a API e o worker não
precisam residir em hosts diferentes neste estágio.

## Decisão

Usar um Compose dedicado ao EasyPanel. PostgreSQL executa com `network_mode: none`,
`listen_addresses=''` e volume nomeado para `/var/run/postgresql`. API, worker e job
de migration montam o socket como read-only. A configuração requer opt-in
`WDE_DATABASE_LOCAL_SOCKET=true` e aceita somente host vazio, diretório exato
`/var/run/postgresql`, `sslmode=disable` e um `passfile` protegido validado no startup.
Sem esse opt-in, produção TCP continua
exigindo `sslmode=verify-full`.

O proxy do EasyPanel termina TLS apenas para `showcase:8080`, `swagger:8080` e
`api:8080`. PostgreSQL, worker, migration e listeners operacionais não recebem porta
ou domínio público. Credenciais e key material entram por secrets do Compose. Um job
`secrets-init`, sem rede e com somente `CAP_CHOWN`, materializa os arquivos `0400` no
UID do processo em volume dedicado; API, worker e migrator montam esse volume em modo
read-only. A vitrine é um binário sem dependência de banco ou segredos.

## Alternativas descartadas

- PostgreSQL TCP sem TLS em bridge privada: expõe uma interface desnecessária e
  enfraquece o contrato fail-closed existente.
- TLS autoassinado no banco do mesmo host: aumenta complexidade operacional e rotação
  sem criar isolamento físico.
- Banco gerenciado externo: melhora HA/backups, mas excede custo e escopo da demo.
- Kubernetes, Redis ou Kafka: não resolvem uma necessidade comprovada neste porte.

## Consequências

O deploy fica restrito a host único e o volume de socket acopla os containers ao
mesmo daemon. Escala horizontal para outro host exige migrar o banco para TCP com
`verify-full` e certificados válidos. O volume de dados requer backup externo e teste
de restore; a topologia não oferece HA. Em troca, o banco não possui superfície TCP,
senhas não entram em DSNs e a CI reproduz o mesmo arranjo antes do deploy.
