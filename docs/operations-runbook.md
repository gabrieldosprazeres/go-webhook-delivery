# Runbook operacional

Este runbook pressupõe acesso autenticado ao ambiente e evidências redigidas. Nunca
copie payloads, API keys, HMAC secrets, keyrings, DSNs, URLs completas ou headers para
ticket, chat, log ou terminal compartilhado. Registre horários UTC, versão/revisão,
contagens agregadas, ação executada e responsável.

## Triagem inicial

1. Preserve logs estruturados, métricas e traces já redigidos; não habilite `pprof` no
   runtime produtivo.
2. Identifique revisão OCI, versão do schema, saúde de `/livez` e `/readyz`, backlog,
   idade mais antiga, DLQ e categorias bounded de erro.
3. Contenha antes de recuperar: retire tráfego, suspenda o workspace ou endpoint e
   bloqueie egress na infraestrutura quando houver suspeita de exfiltração.
4. Faça toda mutação com operador identificado e confirme a auditoria append-only.

## API key exposta

Revogue a chave pelo fluxo administrativo, confirme que chamadas seguintes retornam
`401` e emita uma chave nova com o menor conjunto de scopes. Procure somente pelo
fingerprint/ID opaco e janela temporal na auditoria. Se a chave apareceu em Git,
histórico de shell ou CI, remova-a também desses sistemas e rode `make secret-scan`.
Rotacionar não apaga a necessidade de investigar ações já autorizadas.

## HMAC secret comprometido

Inicie a rotação idempotente com uma chave de comando única e overlap entre uma hora e
sete dias. Distribua a nova chave fora dos logs; consumidores devem aceitar as duas
assinaturas apenas durante a janela e validar timestamp/corpo bruto em tempo constante.
Depois do overlap, confirme purge do envelope anterior e que um retry tardio do comando
não cria outro segredo. Para comprometimento ativo, desabilite o endpoint até todos os
consumidores terem a nova chave.

## Suspeita de SSRF

Desabilite o endpoint e bloqueie egress na camada de rede. Preserve categoria bounded,
timestamp e IDs opacos; não registre a URL. Verifique a revisão em execução e rode a
matriz anti-SSRF contra dados sintéticos. Não crie allowlist temporária para metadata,
rede privada, loopback, link-local, mapped IPv6, NAT64/transições ou DNS misto. Um
destino permitido que depois resolve qualquer IP proibido deve continuar falhando.

## Payload ou chave criptográfica exposta

Suspenda o workspace, revogue API keys e HMAC secrets e substitua o keyring/pepper
afetado. Se a confidencialidade do banco/backup foi perdida, preserve o snapshot sem
colocá-lo em tráfego e siga a quarentena abaixo. Payload expirado deve ser expurgado
pelo job normal; não faça `UPDATE` manual no envelope, pois fencing, attempts e replay
precisam transicionar de forma atômica.

## Banco, fila ou dependência indisponível

`/livez` deve permanecer vivo e `/readyz` deve falhar. Pare novos claims se o banco
estiver instável e deixe leases expirarem; fencing impede finalização obsoleta. Após a
recuperação, acompanhe backlog, idade mais antiga, attempts, retries e DLQ. Se o destino
falhar, mantenha o backoff e `Retry-After`; não aumente concorrência sem medir banco,
egress e capacidade do consumidor.

## Pacote, imagem ou ferramenta comprometida

Interrompa o rollout e coloque os workloads do digest afetado fora de tráfego; não
substitua apenas uma tag mutável. Preserve digest, labels OCI, `images.tsv`, SBOM,
`tools.tsv`, metadata da base Trivy, logs de build e janela de execução. Compare o SBOM
do artefato implantado com o advisory e determine se o componente é alcançável.

Quarentene imagens e caches suspeitos sem apagá-los antes da coleta de evidência. Se a
cadeia comprometida teve acesso a API keys, signing secrets, peppers, keyrings, DSNs ou
tokens de registry/CI, revogue e rotacione o material aplicável; não presuma que rebuild
é suficiente. Refaça o build a partir de runner/base conhecidos, dependências e ações
pinadas, gere novos SBOMs, execute scan no novo digest e repita adversarial/smoke.

Comunique mantenedor, consumidores afetados e operador da infraestrutura com impacto,
digests, intervalo e ação exigida, sem incluir segredos. Só recupere tráfego por digest
novo depois de Code Review/security review e confirmação de que credenciais suspeitas
foram revogadas. Registre causa, alcance, evidências, decisão de divulgação e prevenção.

## Restore e quarentena

API e worker devem permanecer fora do tráfego durante todo o processo:

```bash
go run ./cmd/api restore quarantine --generation <nova-geracao>
go run ./cmd/api restore reconcile --generation <mesma-geracao>
```

A quarentena revoga chaves/segredos, suspende workspaces/endpoints e invalida leases.
Confirme `/readyz` em `503` antes de reconciliar. Só libere tráfego quando reconcile
comprovar ausência de credencial/egress restaurado ativo e novos segredos tiverem sido
distribuídos por canal seguro.

## Rollback

Prefira rollback da aplicação mantendo migrations compatíveis. Nunca execute downgrade
em produção sem backup cifrado verificado, janela de manutenção, revisão do SQL `Down`
e confirmação de que nenhum dado/feature da versão será perdido. Teste o caminho em uma
cópia descartável:

```bash
GOOSE_DRIVER=postgres GOOSE_DBSTRING="$WDE_MIGRATOR_DATABASE_URL" \
  go tool goose -dir db/migrations status
```

O DSN não deve estar na linha de comando nem em evidências. O ensaio automatizado cria
PostgreSQL 17 em `tmpfs`, consulta status, prova `down-to 19 → up 20` vazio, carrega
dados v5 e exige que o wrapper recuse `down-to 0` antes de qualquer mutação. O ensaio
confirma schema físico 20/lógico 5 e preservação dos dados:

```bash
make rollback-rehearsal
```

Se o boundary não for reversível com dados reais, avance com correção forward-only.
Restore exige sempre a quarentena, mesmo quando o backup é anterior à revogação.

## Alertas e encerramento

Alertar sobre readiness, backlog/idade, DLQ, retries, falha de retenção, falha de export
e uso de recursos sem labels de tenant/recurso. Encerrar o incidente somente após
contenção, recuperação, rotação/revogação, auditoria confirmada, backlog estabilizado,
evidências redigidas e ação preventiva registrada.
