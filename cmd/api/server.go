package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openAPIDatabase(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()
	return database.Open(databaseCtx, cfg.DatabaseURL, database.RoleAPI)
}

func serveAPI(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	materials cryptobox.Materials, observability *telemetry.Service, shutdown *appruntime.ShutdownBudget,
) error {
	servers, err := apiHTTPServers(ctx, cfg, pool, materials, observability)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.ServeAllWithBudget(ctx, shutdown, servers...)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func apiHTTPServers(ctx context.Context, cfg config.Config, pool *pgxpool.Pool,
	materials cryptobox.Materials, observability *telemetry.Service,
) ([]appruntime.HTTPServer, error) {
	publicListener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return nil, err
	}
	operationalListener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		_ = publicListener.Close()
		return nil, err
	}
	return []appruntime.HTTPServer{
		{Server: newPublicServer(cfg, pool, materials, observability), Listener: publicListener},
		{Server: newOperationalServer(ctx, cfg, pool, materials, observability), Listener: operationalListener},
	}, nil
}

func newPublicServer(cfg config.Config, pool *pgxpool.Pool, materials cryptobox.Materials,
	optional ...*telemetry.Service,
) *http.Server {
	observability := telemetry.NewNoop()
	if len(optional) == 1 && optional[0] != nil {
		observability = optional[0]
	}
	lookup := auth.NewPostgresLookup(pool)
	deliveryStore := delivery.NewPostgresStore(pool, materials.CursorPepper)
	deps := routesDependencies{
		authenticator: auth.NewAuthenticator(lookup, materials.AuthPepper),
		endpoints:     endpoint.NewHandler(endpoint.NewService(endpoint.NewPostgresStore(pool), cfg.Profile, cfg.AllowHTTPDestinations, materials)),
		events:        event.NewHandler(event.NewService(event.NewPostgresStore(pool), materials)),
		deliveries:    delivery.NewHandler(deliveryStore, delivery.NewReplayService(deliveryStore, materials)),
		limiter:       ratelimit.New(pool, materials.RateLimitPepper),
		edge: ratelimit.NewEdge(ratelimit.EdgePolicy{
			MaxInFlight: cfg.Edge.MaxInFlight, Global: cfg.Edge.Global,
			Origin: cfg.Edge.Origin, Prefix: cfg.Edge.Prefix,
			MaxBuckets: cfg.Edge.MaxBuckets, Window: cfg.Edge.Window,
		}, materials.RateLimitPepper),
		quotas:    cfg.Quotas,
		telemetry: observability,
	}
	return &http.Server{
		Handler: publicSecurityHeaders(publicRoutes(deps),
			cfg.Profile == config.ProfileProduction && cfg.IngressTLSTerminated),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
	}
}

func publicSecurityHeaders(next http.Handler, hsts bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if hsts {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func newOperationalServer(ctx context.Context, cfg config.Config, pool *pgxpool.Pool,
	materials cryptobox.Materials, observability *telemetry.Service,
) *http.Server {
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return database.ErrUnavailable
		}
		if err := materials.Ready(); err != nil {
			return database.ErrUnavailable
		}
		checkCtx, cancel := context.WithTimeout(requestCtx, cfg.DatabaseTimeout)
		defer cancel()
		return database.Check(checkCtx, pool, database.RoleAPI)
	}
	return &http.Server{
		Handler: operational.Handler(readiness, observability.Metrics().Handler()), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10,
	}
}

type routesDependencies struct {
	authenticator *auth.Authenticator
	endpoints     *endpoint.Handler
	events        *event.Handler
	deliveries    *delivery.Handler
	limiter       *ratelimit.Limiter
	edge          *ratelimit.EdgeLimiter
	quotas        config.QuotaConfig
	telemetry     *telemetry.Service
}

func publicRoutes(optional ...routesDependencies) http.Handler {
	mux := http.NewServeMux()
	observability := telemetry.NewNoop()
	if len(optional) == 1 && optional[0].telemetry != nil {
		observability = optional[0].telemetry
	}
	mux.Handle("GET /{$}", observability.HTTP("/", http.HandlerFunc(apiIndex)))
	if len(optional) == 1 {
		deps := optional[0]
		deps.route(mux, "POST /v1/endpoints", "/v1/endpoints", "endpoints:write", "endpoint_write", deps.quotas.EndpointWrite, http.HandlerFunc(deps.endpoints.Create))
		deps.route(mux, "GET /v1/endpoints/{id}", "/v1/endpoints/{id}", "deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.endpoints.Get))
		deps.route(mux, "POST /v1/endpoints/{id}/secret-rotations", "/v1/endpoints/{id}/secret-rotations", "endpoints:write", "endpoint_write", deps.quotas.EndpointWrite, http.HandlerFunc(deps.endpoints.Rotate))
		deps.route(mux, "POST /v1/events", "/v1/events", "events:write", "ingest", deps.quotas.Ingest, http.HandlerFunc(deps.events.Publish))
		deps.route(mux, "GET /v1/deliveries", "/v1/deliveries", "deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.deliveries.List))
		deps.route(mux, "GET /v1/deliveries/{id}", "/v1/deliveries/{id}", "deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.deliveries.Get))
		deps.route(mux, "POST /v1/deliveries/{id}/replays", "/v1/deliveries/{id}/replays", "deliveries:retry", "replay", deps.quotas.Replay, http.HandlerFunc(deps.deliveries.Replay))
	}
	mux.Handle("/", observability.HTTP("unmatched", problem.NotFound()))
	return problem.WithRequestID(problem.Recover(mux))
}

func apiIndex(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("{\"service\":\"webhook-delivery-engine\",\"api_version\":\"v1\",\"status\":\"available\",\"authentication\":\"required\"}\n"))
}

func (deps routesDependencies) route(mux *http.ServeMux, pattern, route, scope, operation string,
	quota config.QuotaPolicy, next http.Handler,
) {
	mux.Handle(pattern, deps.telemetry.HTTP(route, deps.protected(scope, operation, quota, next)))
}

func (deps routesDependencies) protected(scope, operation string, quota config.QuotaPolicy, next http.Handler) http.Handler {
	policy := ratelimit.Policy{
		Operation: operation, Global: quota.Global, Workspace: quota.Workspace,
		APIKey: quota.APIKey, Resource: quota.Resource, Window: quota.Window,
	}
	authorized := deps.authenticator.Authorize(scope, next)
	authenticatedQuota := deps.limiter.Middleware(policy, authorized)
	return deps.edge.Middleware(deps.authenticator.Authenticate(authenticatedQuota))
}
