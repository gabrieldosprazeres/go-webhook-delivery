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
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openAPIDatabase(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()
	return database.Open(databaseCtx, cfg.DatabaseURL, database.RoleAPI)
}

func serveAPI(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, materials cryptobox.Materials) error {
	servers, err := apiHTTPServers(ctx, cfg, pool, materials)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.ServeAll(ctx, cfg.ShutdownTimeout, servers...)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func apiHTTPServers(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, materials cryptobox.Materials) ([]appruntime.HTTPServer, error) {
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
		{Server: newPublicServer(cfg, pool, materials), Listener: publicListener},
		{Server: newOperationalServer(ctx, cfg, pool), Listener: operationalListener},
	}, nil
}

func newPublicServer(cfg config.Config, pool *pgxpool.Pool, materials cryptobox.Materials) *http.Server {
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
		quotas: cfg.Quotas,
	}
	return &http.Server{
		Handler: publicRoutes(deps), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
	}
}

func newOperationalServer(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) *http.Server {
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return database.ErrUnavailable
		}
		checkCtx, cancel := context.WithTimeout(requestCtx, cfg.DatabaseTimeout)
		defer cancel()
		return database.Check(checkCtx, pool, database.RoleAPI)
	}
	return &http.Server{
		Handler: operational.Handler(readiness), ReadHeaderTimeout: 2 * time.Second,
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
}

func publicRoutes(optional ...routesDependencies) http.Handler {
	mux := http.NewServeMux()
	if len(optional) == 1 {
		deps := optional[0]
		mux.Handle("POST /v1/endpoints", deps.protected("endpoints:write", "endpoint_write", deps.quotas.EndpointWrite, http.HandlerFunc(deps.endpoints.Create)))
		mux.Handle("GET /v1/endpoints/{id}", deps.protected("deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.endpoints.Get)))
		mux.Handle("POST /v1/events", deps.protected("events:write", "ingest", deps.quotas.Ingest, http.HandlerFunc(deps.events.Publish)))
		mux.Handle("GET /v1/deliveries", deps.protected("deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.deliveries.List)))
		mux.Handle("GET /v1/deliveries/{id}", deps.protected("deliveries:read", "query", deps.quotas.Query, http.HandlerFunc(deps.deliveries.Get)))
		mux.Handle("POST /v1/deliveries/{id}/replays", deps.protected("deliveries:retry", "replay", deps.quotas.Replay, http.HandlerFunc(deps.deliveries.Replay)))
	}
	mux.Handle("/", problem.NotFound())
	return problem.WithRequestID(problem.Recover(mux))
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
