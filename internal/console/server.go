// Package console exposes the same-origin SSR/BFF experience.
package console

import (
	"context"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/insights"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Dependencies struct {
	Sessions       *consolesession.Service
	Endpoints      *endpoint.Service
	EndpointLister *endpoint.Lister
	Events         *event.Service
	Deliveries     delivery.ViewStore
	Replay         *delivery.ReplayService
	Insights       insights.Store
	Origin         string
	SecureCookies  bool
	Telemetry      *telemetry.Service
	LoginGuard     func(http.Handler) http.Handler
	Limiter        interface {
		Consume(context.Context, auth.Principal, uuid.UUID, ratelimit.Policy) error
	}
	QueryPolicy, IngestPolicy, EndpointPolicy, ReplayPolicy ratelimit.Policy
}

type Server struct {
	deps      Dependencies
	templates *template.Template
}

func New(deps Dependencies) *Server {
	tmpl := template.Must(template.New("pages").Funcs(template.FuncMap{
		"date": func(value time.Time) string { return value.Local().Format("02/01/2006 15:04:05") },
		"short": func(value any) string {
			text := template.HTMLEscapeString(toString(value))
			if len(text) > 12 {
				return text[:12] + "…"
			}
			return text
		},
	}).ParseFS(assets, "templates/*.html"))
	return &Server{deps: deps, templates: tmpl}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /login", s.loginPage)
	loginHandler := http.Handler(http.HandlerFunc(s.login))
	if s.deps.LoginGuard != nil {
		loginHandler = s.deps.LoginGuard(loginHandler)
	}
	mux.Handle("POST /session", loginHandler)
	mux.Handle("POST /session/logout", s.require("", http.HandlerFunc(s.logout)))
	mux.Handle("GET /app", s.require("deliveries:read", http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /app/metrics", s.require("deliveries:read", http.HandlerFunc(s.metrics)))
	mux.Handle("GET /app/endpoints", s.require("deliveries:read", http.HandlerFunc(s.endpointList)))
	mux.Handle("GET /app/endpoints/new", s.require("endpoints:write", http.HandlerFunc(s.endpointNew)))
	mux.Handle("POST /app/endpoints", s.require("endpoints:write", http.HandlerFunc(s.endpointCreate)))
	mux.Handle("GET /app/endpoints/{id}", s.require("deliveries:read", http.HandlerFunc(s.endpointDetail)))
	mux.Handle("GET /app/events/new", s.require("events:write", http.HandlerFunc(s.eventNew)))
	mux.Handle("POST /app/events", s.require("events:write", http.HandlerFunc(s.eventPublish)))
	mux.Handle("GET /app/deliveries", s.require("deliveries:read", http.HandlerFunc(s.deliveryList)))
	mux.Handle("GET /app/deliveries/{id}", s.require("deliveries:read", http.HandlerFunc(s.deliveryDetail)))
	mux.Handle("POST /app/deliveries/{id}/replays", s.require("deliveries:retry", http.HandlerFunc(s.deliveryReplay)))
	mux.Handle("GET /static/", http.FileServerFS(assets))
	mux.HandleFunc("/", http.NotFound)
	handler := http.Handler(mux)
	if s.deps.Telemetry != nil {
		handler = s.deps.Telemetry.HTTP("console", handler)
	}
	return s.securityHeaders(handler)
}

func toString(value any) string {
	if stringer, ok := value.(interface{ String() string }); ok {
		return stringer.String()
	}
	return ""
}
