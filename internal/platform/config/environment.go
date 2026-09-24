package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func applyEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	applyStringEnvironment(cfg, lookup)
	var err error
	if cfg.ShutdownTimeout, err = durationEnv(lookup, "WDE_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	if cfg.DatabaseTimeout, err = durationEnv(lookup, "WDE_DATABASE_TIMEOUT", cfg.DatabaseTimeout); err != nil {
		return err
	}
	if cfg.IngressTLSTerminated, err = boolEnv(lookup, "WDE_INGRESS_TLS_TERMINATED", false); err != nil {
		return err
	}
	if cfg.EnablePprof, err = boolEnv(lookup, "WDE_ENABLE_PPROF", false); err != nil {
		return err
	}
	if cfg.AllowHTTPDestinations, err = boolEnv(lookup, "WDE_ALLOW_HTTP_DESTINATIONS", false); err != nil {
		return err
	}
	if err := applyQuotaEnvironment(cfg, lookup); err != nil {
		return err
	}
	if err := applyEdgeEnvironment(cfg, lookup); err != nil {
		return err
	}
	if cfg.Service == ServiceWorker {
		if err := applyWorkerEnvironment(cfg, lookup); err != nil {
			return err
		}
	}
	applySecretPaths(cfg, lookup)
	return nil
}

func applyWorkerEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	integers := []struct {
		name   string
		target *int
	}{{"WDE_WORKER_CONCURRENCY", &cfg.WorkerConcurrency}, {"WDE_WORKER_CLAIM_BATCH_SIZE", &cfg.WorkerClaimBatchSize},
		{"WDE_WORKER_WORKSPACE_BATCH_LIMIT", &cfg.WorkerWorkspaceLimit}, {"WDE_WORKER_ENDPOINT_BATCH_LIMIT", &cfg.WorkerEndpointLimit}}
	for _, item := range integers {
		value, err := intEnv(lookup, item.name, *item.target)
		if err != nil {
			return err
		}
		*item.target = value
	}
	durations := []struct {
		name   string
		target *time.Duration
	}{{"WDE_WORKER_POLL_INTERVAL", &cfg.WorkerPollInterval}, {"WDE_WORKER_CLAIM_TIMEOUT", &cfg.WorkerClaimTimeout},
		{"WDE_WORKER_REQUEST_TIMEOUT", &cfg.WorkerRequestTimeout},
		{"WDE_WORKER_LEASE_TTL", &cfg.WorkerLeaseTTL}, {"WDE_WORKER_RETRY_BASE", &cfg.WorkerRetryBase},
		{"WDE_WORKER_RETRY_CAP", &cfg.WorkerRetryCap}}
	for _, item := range durations {
		value, err := durationEnv(lookup, item.name, *item.target)
		if err != nil {
			return err
		}
		*item.target = value
	}
	return nil
}

func applyStringEnvironment(cfg *Config, lookup func(string) (string, bool)) {
	if value, ok := lookup("WDE_PROFILE"); ok {
		cfg.Profile = Profile(value)
	}
	assignEnv(lookup, "WDE_VERSION", &cfg.Version)
	assignEnv(lookup, "WDE_LOG_LEVEL", &cfg.LogLevel)
	assignEnv(lookup, "WDE_DATABASE_URL", &cfg.DatabaseURL)
	assignEnv(lookup, httpAddrVariable(cfg.Service), &cfg.HTTPAddr)
	assignEnv(lookup, operationalAddrVariable(cfg.Service), &cfg.OperationalAddr)
}

func assignEnv(lookup func(string) (string, bool), name string, target *string) {
	if value, ok := lookup(name); ok {
		*target = value
	}
}

func applyFlags(cfg *Config, args []string) error {
	flags := flag.NewFlagSet(string(cfg.Service), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	profile := flags.String("profile", string(cfg.Profile), "runtime profile")
	httpAddr := flags.String("http-addr", cfg.HTTPAddr, "HTTP listen address")
	operationalAddr := flags.String("operational-addr", cfg.OperationalAddr, "operational HTTP listen address")
	shutdownTimeout := flags.Duration("shutdown-timeout", cfg.ShutdownTimeout, "graceful shutdown timeout")
	databaseTimeout := flags.Duration("database-timeout", cfg.DatabaseTimeout, "database operation timeout")
	logLevel := flags.String("log-level", cfg.LogLevel, "log level")
	version := flags.String("version", cfg.Version, "build version")
	ingressTLS := flags.Bool("ingress-tls-terminated", cfg.IngressTLSTerminated, "declare trusted ingress TLS termination")
	pprof := flags.Bool("enable-pprof", cfg.EnablePprof, "enable local profiling")
	allowHTTP := flags.Bool("allow-http-destinations", cfg.AllowHTTPDestinations, "allow HTTP destinations outside production")
	workerConcurrency := flags.Int("worker-concurrency", cfg.WorkerConcurrency, "maximum concurrent deliveries")
	workerBatch := flags.Int("worker-claim-batch-size", cfg.WorkerClaimBatchSize, "maximum claims per poll")
	workerWorkspaceLimit := flags.Int("worker-workspace-batch-limit", cfg.WorkerWorkspaceLimit, "claims per workspace in one batch")
	workerEndpointLimit := flags.Int("worker-endpoint-batch-limit", cfg.WorkerEndpointLimit, "claims per endpoint in one batch")
	workerPoll := flags.Duration("worker-poll-interval", cfg.WorkerPollInterval, "empty queue poll interval")
	workerClaimTimeout := flags.Duration("worker-claim-timeout", cfg.WorkerClaimTimeout, "claim database deadline")
	workerRequestTimeout := flags.Duration("worker-request-timeout", cfg.WorkerRequestTimeout, "outbound request timeout")
	workerLeaseTTL := flags.Duration("worker-lease-ttl", cfg.WorkerLeaseTTL, "delivery lease duration")
	workerRetryBase := flags.Duration("worker-retry-base", cfg.WorkerRetryBase, "retry exponential base")
	workerRetryCap := flags.Duration("worker-retry-cap", cfg.WorkerRetryCap, "retry maximum delay")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("config: unexpected positional argument")
	}
	applyParsedFlags(cfg, *profile, *httpAddr, *operationalAddr, *logLevel, *version)
	cfg.ShutdownTimeout, cfg.DatabaseTimeout = *shutdownTimeout, *databaseTimeout
	cfg.IngressTLSTerminated, cfg.EnablePprof, cfg.AllowHTTPDestinations = *ingressTLS, *pprof, *allowHTTP
	cfg.WorkerConcurrency, cfg.WorkerClaimBatchSize = *workerConcurrency, *workerBatch
	cfg.WorkerWorkspaceLimit, cfg.WorkerEndpointLimit = *workerWorkspaceLimit, *workerEndpointLimit
	cfg.WorkerPollInterval, cfg.WorkerClaimTimeout = *workerPoll, *workerClaimTimeout
	cfg.WorkerRequestTimeout = *workerRequestTimeout
	cfg.WorkerLeaseTTL, cfg.WorkerRetryBase, cfg.WorkerRetryCap = *workerLeaseTTL, *workerRetryBase, *workerRetryCap
	return nil
}

func applyParsedFlags(cfg *Config, profile, httpAddr, operationalAddr, logLevel, version string) {
	cfg.Profile = Profile(profile)
	cfg.HTTPAddr, cfg.OperationalAddr = httpAddr, operationalAddr
	cfg.LogLevel, cfg.Version = logLevel, version
}

func boolEnv(lookup func(string) (string, bool), name string, fallback bool) (bool, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean", name)
	}
	return parsed, nil
}

func durationEnv(lookup func(string) (string, bool), name string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a duration", name)
	}
	return parsed, nil
}

func intEnv(lookup func(string) (string, bool), name string, fallback int) (int, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer", name)
	}
	return parsed, nil
}

func envValue(lookup func(string) (string, bool), name string) (string, bool) {
	value, ok := lookup(name)
	return strings.TrimSpace(value), ok
}

func httpAddrVariable(service Service) string {
	if service == ServiceChaosLab {
		return "WDE_CHAOSLAB_HTTP_ADDR"
	}
	return "WDE_API_HTTP_ADDR"
}

func operationalAddrVariable(service Service) string {
	if service == ServiceWorker {
		return "WDE_WORKER_OPERATIONAL_ADDR"
	}
	return "WDE_API_OPERATIONAL_ADDR"
}
