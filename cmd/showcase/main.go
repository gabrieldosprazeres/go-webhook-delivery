package main

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

const defaultAddress = ":8080"

type pageData struct {
	APIURL      string
	DocsURL     string
	GitHubURL   string
	ReleaseURL  string
	LinkedInURL string
	Version     string
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("showcase: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	address := environment("WDE_SHOWCASE_HTTP_ADDR", defaultAddress)
	if len(args) == 1 && args[0] == "healthcheck" {
		return operational.CheckLive(parent, healthcheckAddress(address))
	}
	if len(args) != 0 {
		return errors.New("showcase: invalid arguments")
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           routes(loadPageData()),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	ctx, stop := appruntime.SignalContext(parent)
	defer stop()
	slog.InfoContext(ctx, "showcase started", "address", address)
	return appruntime.Serve(ctx, server, listener, 10*time.Second)
}

func loadPageData() pageData {
	return pageData{
		APIURL:      environment("WDE_SHOWCASE_API_URL", "https://api.example.com"),
		DocsURL:     environment("WDE_SHOWCASE_DOCS_URL", "https://docs.example.com"),
		GitHubURL:   environment("WDE_SHOWCASE_GITHUB_URL", "https://github.com/gabrieldosprazeres/go-webhook-delivery"),
		ReleaseURL:  environment("WDE_SHOWCASE_RELEASE_URL", "https://github.com/gabrieldosprazeres/go-webhook-delivery/releases"),
		LinkedInURL: environment("WDE_SHOWCASE_LINKEDIN_URL", "https://www.linkedin.com/in/gabrieldosprazeres"),
		Version:     environment("WDE_VERSION", "dev"),
	}
}

func routes(data pageData) http.Handler {
	tmpl := template.Must(template.New("showcase").Parse(pageTemplate))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(response, data); err != nil {
			slog.Error("showcase render failed", "error", err)
		}
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func healthcheckAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

const pageTemplate = `<!doctype html>
<html lang="pt-BR">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <meta name="description" content="Case técnico em Go: uma engine durável e segura para entrega de webhooks.">
  <title>Webhook Delivery Engine — Case em Go</title>
  <style>
    :root{color-scheme:dark;--bg:#07110f;--card:#0d1c19;--line:#21453d;--text:#f4fbf8;--muted:#a8beb7;--mint:#62f5b4;--cyan:#68d7ff;--orange:#ffb86b;--max:1120px}
    *{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:radial-gradient(circle at 75% 0,#123b32 0,transparent 34rem),var(--bg);color:var(--text);font:16px/1.65 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    a{color:inherit}.wrap{width:min(var(--max),calc(100% - 2rem));margin:auto}.nav{display:flex;align-items:center;justify-content:space-between;padding:1.25rem 0}.brand{font-weight:800;letter-spacing:-.03em}.brand span{color:var(--mint)}.navlinks{display:flex;gap:.8rem;align-items:center}.navlinks a{text-decoration:none;color:var(--muted)}.button{display:inline-flex;align-items:center;justify-content:center;padding:.76rem 1rem;border:1px solid var(--line);border-radius:.75rem;text-decoration:none;font-weight:750;background:#10231f}.button.primary{background:var(--mint);color:#062019;border-color:var(--mint)}
    .hero{padding:7rem 0 4.5rem;display:grid;grid-template-columns:1.25fr .75fr;gap:4rem;align-items:center}.eyebrow{color:var(--mint);font:700 .78rem/1 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.13em;text-transform:uppercase}.hero h1{font-size:clamp(3rem,7vw,6.5rem);line-height:.91;letter-spacing:-.075em;margin:.9rem 0 1.5rem;max-width:850px}.hero p{font-size:1.15rem;color:var(--muted);max-width:670px}.actions{display:flex;flex-wrap:wrap;gap:.75rem;margin-top:2rem}.terminal{border:1px solid var(--line);border-radius:1rem;background:#081511;box-shadow:0 30px 80px #0007;overflow:hidden}.terminal .top{height:2.5rem;background:#10231f;border-bottom:1px solid var(--line);display:flex;align-items:center;gap:.45rem;padding:0 .8rem}.dot{width:.62rem;height:.62rem;border-radius:50%;background:var(--orange)}.dot:nth-child(2){background:#ffe07a}.dot:nth-child(3){background:var(--mint)}pre{margin:0;padding:1.4rem;overflow:auto;color:#c9ffe8;font:500 .79rem/1.8 ui-monospace,SFMono-Regular,Menlo,monospace}.code-muted{color:#63837a}.code-cyan{color:var(--cyan)}
    section{padding:5rem 0}.section-title{font-size:clamp(2rem,4vw,3.5rem);letter-spacing:-.055em;line-height:1;margin:0 0 1rem}.lead{color:var(--muted);max-width:720px}.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:1rem;margin-top:2rem}.card{padding:1.45rem;border:1px solid var(--line);border-radius:1rem;background:linear-gradient(145deg,#0e201c,#0a1714)}.card strong{display:block;font-size:1.05rem;margin-bottom:.45rem}.card p{color:var(--muted);margin:0}.number{font:800 2rem/1 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--mint);margin-bottom:1.2rem}.flow{display:grid;grid-template-columns:repeat(4,1fr);gap:1px;background:var(--line);border:1px solid var(--line);border-radius:1rem;overflow:hidden;margin-top:2.5rem}.step{background:var(--card);padding:1.5rem}.step small{color:var(--mint);font-weight:800}.step h3{margin:.5rem 0}.step p{margin:0;color:var(--muted);font-size:.92rem}
    .proof{display:grid;grid-template-columns:1fr 1fr;gap:1rem}.quote{font-size:1.7rem;line-height:1.35;letter-spacing:-.035em}.checks{list-style:none;padding:0;margin:0}.checks li{padding:.8rem 0;border-bottom:1px solid var(--line);color:var(--muted)}.checks li::before{content:"✓";color:var(--mint);font-weight:900;margin-right:.7rem}.cta{text-align:center;border:1px solid var(--line);border-radius:1.2rem;padding:4rem 1rem;background:radial-gradient(circle at 50% 120%,#1a5947,transparent 55%),var(--card)}.cta p{color:var(--muted)}footer{padding:2.5rem 0;color:var(--muted);font-size:.9rem;display:flex;justify-content:space-between;border-top:1px solid var(--line)}
    @media(max-width:820px){.navlinks a:not(.button){display:none}.hero{grid-template-columns:1fr;padding-top:4rem;gap:2rem}.terminal{transform:none}.grid,.flow,.proof{grid-template-columns:1fr}.hero h1{font-size:3.6rem}section{padding:3.5rem 0}footer{gap:1rem;flex-direction:column}}
  </style>
</head>
<body>
  <header class="wrap nav"><div class="brand">webhook<span>.</span>engine</div><nav class="navlinks"><a href="#arquitetura">Arquitetura</a><a href="#engenharia">Engenharia</a><a class="button" href="{{.GitHubURL}}" target="_blank" rel="noopener noreferrer">Ver código ↗</a></nav></header>
  <main>
    <section class="wrap hero">
      <div><div class="eyebrow">Case técnico · Go + PostgreSQL</div><h1>Webhooks que chegam. Mesmo quando tudo falha.</h1><p>Uma engine de entrega durável com semântica <em>at-least-once</em>, retentativas inteligentes, isolamento multi-tenant e segurança aplicada desde a arquitetura.</p><div class="actions"><a class="button primary" href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">Explorar API no Swagger</a><a class="button" href="{{.GitHubURL}}" target="_blank" rel="noopener noreferrer">GitHub</a><a class="button" href="{{.ReleaseURL}}" target="_blank" rel="noopener noreferrer">Release</a></div></div>
      <div class="terminal" aria-label="Exemplo de entrega"><div class="top"><span class="dot"></span><span class="dot"></span><span class="dot"></span></div><pre><span class="code-muted">POST</span> <span class="code-cyan">/v1/events</span>
Authorization: Bearer wde_...
Idempotency-Key: checkout_8472

{
  "type": "order.paid",
  "data": { "order_id": "ord_8472" }
}

<span class="code-muted">HTTP/1.1</span> <span class="code-cyan">202 Accepted</span>
{
  "event_id": "evt_01...",
  "status": "accepted"
}</pre></div>
    </section>
    <section id="engenharia" class="wrap"><div class="eyebrow">O que este case demonstra</div><h2 class="section-title">Backend de produção, não CRUD de tutorial.</h2><p class="lead">O projeto concentra problemas reais de sistemas distribuídos e deixa as decisões visíveis em testes, ADRs, contrato OpenAPI e automação de entrega.</p><div class="grid">
      <article class="card"><div class="number">01</div><strong>Durabilidade transacional</strong><p>Evento e entregas elegíveis persistem atomicamente no PostgreSQL antes do 202.</p></article>
      <article class="card"><div class="number">02</div><strong>Concorrência segura</strong><p>Leases, fencing tokens e SKIP LOCKED protegem a fila contra workers concorrentes.</p></article>
      <article class="card"><div class="number">03</div><strong>Defesa em profundidade</strong><p>SSRF, HMAC, criptografia de payload, least privilege e logs sem dados sensíveis.</p></article>
      <article class="card"><div class="number">04</div><strong>Confiabilidade observável</strong><p>Métricas, traces, readiness dinâmica, dead letter e histórico de tentativas.</p></article>
      <article class="card"><div class="number">05</div><strong>Contrato profissional</strong><p>OpenAPI versionado, erros Problem Details, idempotência e paginação autenticada.</p></article>
      <article class="card"><div class="number">06</div><strong>Entrega verificável</strong><p>CI, race detector, scanners, SBOM, imagens distroless e migrations explícitas.</p></article>
    </div></section>
    <section id="arquitetura" class="wrap"><div class="eyebrow">Fluxo de entrega</div><h2 class="section-title">Simples por fora. Rigoroso por dentro.</h2><div class="flow">
      <div class="step"><small>01 · INGEST</small><h3>API recebe</h3><p>Autentica workspace, limita quota e valida idempotência.</p></div>
      <div class="step"><small>02 · COMMIT</small><h3>Postgres persiste</h3><p>Registra evento e fan-out em uma transação atômica.</p></div>
      <div class="step"><small>03 · CLAIM</small><h3>Worker agenda</h3><p>Seleciona trabalho com fairness, lease e fencing token.</p></div>
      <div class="step"><small>04 · DELIVER</small><h3>Destino confirma</h3><p>Assina, envia, registra resultado e agenda nova tentativa.</p></div>
    </div></section>
    <section class="wrap proof"><div class="card quote">“O objetivo não é fingir complexidade. É mostrar que eu sei onde ela existe e como controlá-la.”</div><div class="card"><ul class="checks"><li>Monólito modular com binários separados</li><li>20 migrations versionadas</li><li>Testes unitários, integração e adversariais</li><li>ADRs para decisões arquiteturais</li><li>Runbooks de operação e recuperação</li></ul></div></section>
    <section class="wrap"><div class="cta"><div class="eyebrow">Demonstração navegável</div><h2 class="section-title">Veja o contrato. Leia as decisões. Rode os testes.</h2><p>O ambiente público usa somente dados sintéticos e existe para demonstrar engenharia.</p><div class="actions" style="justify-content:center"><a class="button primary" href="{{.DocsURL}}" target="_blank" rel="noopener noreferrer">Abrir Swagger</a><a class="button" href="{{.APIURL}}" target="_blank" rel="noopener noreferrer">Endpoint da API</a><a class="button" href="{{.LinkedInURL}}" target="_blank" rel="noopener noreferrer">LinkedIn</a></div></div></section>
  </main>
  <footer class="wrap"><span>Desenvolvido por Gabriel dos Prazeres.</span><span>Versão {{.Version}} · Go 1.27 · PostgreSQL 17</span></footer>
</body>
</html>`
