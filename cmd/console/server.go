package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	webconsole "github.com/gabrieldosprazeres/go-webhook-delivery/internal/console"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/insights"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openConsoleDatabase(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	return database.Open(ctx, cfg.DatabaseURL, database.RoleConsole)
}

func serveConsole(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool,
	materials cryptobox.Materials, observability *telemetry.Service, shutdown *appruntime.ShutdownBudget,
) error {
	publicListener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	operationalListener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		_ = publicListener.Close()
		return err
	}
	lookup := auth.NewPostgresLookup(pool)
	authenticator := auth.NewAuthenticator(lookup, materials.AuthPepper)
	sessionService := consolesession.New(consolesession.NewPostgresStore(pool), authenticator,
		materials.SessionPepper, materials.CSRFPepper)
	endpointStore := endpoint.NewPostgresStore(pool)
	deliveryStore := delivery.NewPostgresStore(pool, materials.CursorPepper)
	loginLimiter := ratelimit.NewEdge(ratelimit.EdgePolicy{
		MaxInFlight: 32, Global: 120, Origin: 120, Prefix: 10, MaxBuckets: 1024, Window: time.Minute,
	}, materials.RateLimitPepper)
	quotaLimiter := ratelimit.New(pool, materials.RateLimitPepper)
	handler := webconsole.New(webconsole.Dependencies{
		Sessions: sessionService, Endpoints: endpoint.NewService(endpointStore, cfg.Profile, cfg.AllowHTTPDestinations, materials),
		EndpointLister: endpoint.NewLister(endpointStore, materials), Events: event.NewService(event.NewPostgresStore(pool), materials),
		Deliveries: deliveryStore, Replay: delivery.NewReplayService(deliveryStore, materials),
		Insights: insights.NewPostgresStore(pool), Origin: cfg.Console.Origin,
		SecureCookies: cfg.Profile == config.ProfileProduction, Telemetry: observability,
		LoginGuard:     loginLimiter.LoginMiddleware,
		Limiter:        quotaLimiter,
		QueryPolicy:    quotaPolicy("query", cfg.Quotas.Query),
		IngestPolicy:   quotaPolicy("ingest", cfg.Quotas.Ingest),
		EndpointPolicy: quotaPolicy("endpoint_write", cfg.Quotas.EndpointWrite),
		ReplayPolicy:   quotaPolicy("replay", cfg.Quotas.Replay),
	}).Handler()
	publicServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil || materials.Ready() != nil {
			return database.ErrUnavailable
		}
		checkCtx, cancel := context.WithTimeout(requestCtx, cfg.DatabaseTimeout)
		defer cancel()
		return database.Check(checkCtx, pool, database.RoleConsole)
	}
	operationalServer := &http.Server{Handler: operational.Handler(readiness, observability.Metrics().Handler()),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.ServeAllWithBudget(ctx, shutdown,
		appruntime.HTTPServer{Server: publicServer, Listener: publicListener},
		appruntime.HTTPServer{Server: operationalServer, Listener: operationalListener})
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func quotaPolicy(operation string, policy config.QuotaPolicy) ratelimit.Policy {
	return ratelimit.Policy{Operation: operation, Global: policy.Global, Workspace: policy.Workspace,
		APIKey: policy.APIKey, Resource: policy.Resource, Window: policy.Window}
}
