# Benchmark e soak da Sprint 6

Resultados locais de 2026-09-24 no commit base
`5dcab024affb3d72d616a39694bdf76de1872122`. Eles não representam promessa de SLO.

## Ambiente e método

- Mac mini, Apple M4 com 10 CPUs e 16 GiB RAM, macOS 26.6.2 arm64;
- Go 1.27.1 darwin/arm64, Docker 29.5.2 e Compose 5.5.1;
- PostgreSQL 17 em container local com storage descartável em `tmpfs` de 1 GiB;
- API, worker e receptor HTTP loopback compilados localmente;
- payload sintético pequeno e HMAC verificado em todas as entregas;
- worker com concorrência/batch/workspace/endpoint `100/100/100/100`, request timeout
  de 1 s, lease de 20 s e claim timeout de 2 s.

Cada rodada cria banco e processos novos. Um warmup separado de 50 eventos é concluído
e excluído das estatísticas; o benchmark normal exige no mínimo três rodadas medidas.
O receptor conhece previamente o conjunto de `delivery_id`, conta IDs únicos,
duplicatas, ausentes e inesperados, e só sinaliza conclusão após receber todos os IDs
distintos. O banco precisa confirmar `backlog=0`, exatamente o total esperado em
`succeeded` e zero falhas.

O harness coleta RSS da API/worker e backlog a cada 250 ms, além de goroutines/heap do
gerador/receptor a cada 500 ms e cinco amostras depois de encerrar o receptor e forçar
GC. Os limites fail-closed são +128 MiB de RSS, +16 MiB de heap, +6 goroutines e
ausência de crescimento estritamente monotônico acima de 32 MiB de RSS nas cinco
amostras finais ou acima de 1 MiB de heap no pós-GC. O teto físico é 5.000 eventos,
inclusive quando variáveis de ambiente pedem mais.

## Benchmark normal

Warmup excluído: 50 eventos. Dataset medido: três rodadas independentes de 1.000
eventos, concorrência de ingestão 4.

| Rodada | Ingestão/s | p95 ingestão | Delivery/s | Unique/dup/missing | DB final |
|---:|---:|---:|---:|---:|---:|
| 1 | 440,00 | 15,08 ms | 33,67 | 1.000/0/0 | 0 backlog; 1.000 succeeded |
| 2 | 475,36 | 10,78 ms | 32,31 | 1.000/0/0 | 0 backlog; 1.000 succeeded |
| 3 | 411,83 | 15,32 ms | 32,77 | 1.000/0/0 | 0 backlog; 1.000 succeeded |

| Métrica | Mínimo | Máximo | Mediana | Média | Desvio padrão | p95 das rodadas |
|---|---:|---:|---:|---:|---:|---:|
| Ingestão/s | 411,83 | 475,36 | 440,00 | 442,39 | 25,99 | 475,36 |
| p95 ingestão (ms) | 10,78 | 15,32 | 15,08 | 13,73 | 2,08 | 15,32 |
| Delivery/s | 32,31 | 33,67 | 32,77 | 32,91 | 0,56 | 33,67 |

As rodadas produziram 75, 80 e 78 amostras de sistema. API RSS terminou abaixo do
primeiro ponto em todas; worker variou, respectivamente, 23,84→26,55 MiB,
24,47→26,13 MiB e 21,06→25,94 MiB. Após teardown/GC, o gerador voltou a uma goroutine
e 571–586 KiB de heap. Todos os thresholds passaram.

O NFR de ingestão (≥100/s e p95 <200 ms) passou. O alvo de delivery (≥100/s) **não
passou**: a mediana foi 32,77/s. A divergência permanece risco conhecido para investigar
com plano SQL, frequência de polling/claims, round-trips de finalização e pressão de
fsync antes de prometer capacidade produtiva.

## Soak bounded

O soak publicado usa 3.000 eventos — não o teto de 5.000 —, concorrência 4 e budget de
quatro minutos. Ele concluiu 3.000 IDs únicos, zero duplicata, missing, inesperado,
assinatura inválida ou falha e backlog final zero. O snapshot foi feito após fechar o
listener e drenar handlers. Ingestão foi 491,85/s com p95 11,86 ms; delivery foi
23,54/s em 127,40 s.

Foram 283 amostras de sistema: API RSS 26,02→14,70 MiB, worker RSS
23,41→26,56 MiB e backlog 2.992→0. O gerador/receptor teve 271 amostras durante a
execução e cinco pós-teardown/GC; goroutines 1→1 e heap 228 KiB→1,01 MiB. Nenhum
threshold de crescimento ou monotonia falhou. Isso é evidência bounded nesta carga,
não prova ausência universal de leak nem valida 5.000 eventos.

## Reprodução

```bash
make benchmark
WDE_SOAK_EVENTS=3000 WDE_SOAK_CONCURRENCY=4 WDE_SOAK_TIMEOUT=4m make soak
```

Para persistir o JSON completo, que inclui cada rodada e todas as séries, use um caminho
novo e absoluto:

```bash
WDE_BENCH_OUTPUT=/caminho/novo/benchmark.json make benchmark
```

O script cria projeto Compose, portas, credencial `0600`, banco e diretório temporários,
recusa URL não-loopback, não imprime segredos e remove exatamente os recursos criados.
Não compare números obtidos com dataset, hardware ou configuração diferentes sem
publicar essas diferenças.
