# Política de segurança

## Versões suportadas

A linha `1.x` mais recente recebe correções de segurança. Versões anteriores devem ser
atualizadas antes de receber suporte. O projeto de portfólio não possui prazo formal
de suporte ou SLA.

## Como reportar

Não abra uma issue pública com detalhes exploráveis, credenciais, payloads ou dados de
terceiros. Envie um relato privado ao mantenedor pelo canal privado indicado no perfil
do repositório, contendo versão/commit, impacto, pré-condições e uma reprodução mínima
com dados sintéticos. O recebimento será confirmado e a severidade, o prazo e a forma
de divulgação serão coordenados antes de qualquer publicação.

Nunca envie API keys, HMAC secrets, peppers, keyrings, DSNs, SBOMs privados, dumps ou
logs não redigidos. Revogue imediatamente qualquer credencial exposta.

## Premissas e limites

- O serviço deve operar atrás de TLS de entrada, rede privada para PostgreSQL, ACL na
  superfície operacional e egress firewall como defesa adicional.
- Produção exige keyrings e peppers distintos, externos ao banco, em arquivos `0600`.
- Destinos produtivos são HTTPS e passam pelo cliente anti-SSRF; o Chaos Lab e HTTP
  loopback são recursos exclusivamente locais.
- Payload, segredo, URL completa, headers e resposta externa não são observáveis.
- O MVP não é um serviço SaaS certificado, não oferece HA/multi-região e não substitui
  secret manager, KMS/HSM, WAF ou monitoramento da infraestrutura.

O modelo de ameaças e os controles normativos estão em
[`docs/webhook-delivery-engine-architecture.md`](docs/webhook-delivery-engine-architecture.md).
O procedimento operacional está em [`docs/operations-runbook.md`](docs/operations-runbook.md).
