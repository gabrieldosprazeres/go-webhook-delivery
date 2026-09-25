package config

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

const productionDatabaseSocket = "/var/run/postgresql"

// Validate applies process- and profile-specific fail-closed rules.
func (cfg Config) Validate() error {
	if err := cfg.validateCommon(); err != nil {
		return err
	}
	if cfg.Profile != ProfileProduction {
		return nil
	}
	return cfg.validateProduction()
}

func (cfg Config) validateCommon() error {
	switch cfg.Profile {
	case ProfileLocal, ProfileTest, ProfileProduction:
	default:
		return errors.New("config: WDE_PROFILE must be local, test or production")
	}
	if strings.TrimSpace(cfg.Version) == "" {
		return errors.New("config: WDE_VERSION must not be empty")
	}
	if !validLogLevel(cfg.LogLevel) {
		return errors.New("config: WDE_LOG_LEVEL must be debug, info, warn or error")
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout > maxShutdownTimeout {
		return errors.New("config: WDE_SHUTDOWN_TIMEOUT must be between 1ns and 30s")
	}
	if cfg.DatabaseTimeout <= 0 || cfg.DatabaseTimeout > maxDatabaseTimeout {
		return errors.New("config: WDE_DATABASE_TIMEOUT must be between 1ns and 30s")
	}
	if cfg.Service == ServiceWorker {
		if err := cfg.validateWorker(); err != nil {
			return err
		}
	}
	if err := cfg.validateQuotas(); err != nil {
		return err
	}
	if err := cfg.validateEdge(); err != nil {
		return err
	}
	if err := cfg.validateTelemetry(); err != nil {
		return err
	}
	return cfg.validateAddresses()
}

func (cfg Config) validateWorker() error {
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 100 {
		return errors.New("config: WDE_WORKER_CONCURRENCY must be between 1 and 100")
	}
	if cfg.WorkerClaimBatchSize < 1 || cfg.WorkerClaimBatchSize > cfg.WorkerConcurrency {
		return errors.New("config: WDE_WORKER_CLAIM_BATCH_SIZE must be between 1 and worker concurrency")
	}
	if cfg.WorkerWorkspaceLimit < 1 || cfg.WorkerWorkspaceLimit > cfg.WorkerClaimBatchSize ||
		cfg.WorkerEndpointLimit < 1 || cfg.WorkerEndpointLimit > cfg.WorkerClaimBatchSize {
		return errors.New("config: worker fairness limits must be between 1 and claim batch size")
	}
	if cfg.WorkerPollInterval < 10*time.Millisecond || cfg.WorkerPollInterval > 10*time.Second {
		return errors.New("config: WDE_WORKER_POLL_INTERVAL must be between 10ms and 10s")
	}
	if cfg.WorkerClaimTimeout < 10*time.Millisecond || cfg.WorkerClaimTimeout > 2*time.Second ||
		cfg.WorkerClaimTimeout > cfg.WorkerPollInterval || cfg.WorkerClaimTimeout > cfg.WorkerLeaseTTL {
		return errors.New("config: WDE_WORKER_CLAIM_TIMEOUT must be between 10ms and min(2s, poll interval, lease TTL)")
	}
	if cfg.WorkerRequestTimeout < 100*time.Millisecond || cfg.WorkerRequestTimeout > 20*time.Second {
		return errors.New("config: WDE_WORKER_REQUEST_TIMEOUT must be between 100ms and 20s")
	}
	if cfg.WorkerLeaseTTL < cfg.WorkerRequestTimeout+10*time.Second || cfg.WorkerLeaseTTL > 2*time.Minute {
		return errors.New("config: WDE_WORKER_LEASE_TTL must exceed request timeout by 10s and be at most 2m")
	}
	if cfg.WorkerRetryBase <= 0 || cfg.WorkerRetryBase > cfg.WorkerRetryCap || cfg.WorkerRetryCap > 15*time.Minute {
		return errors.New("config: worker retry base/cap are invalid")
	}
	if cfg.ShutdownTimeout > 30*time.Second {
		return errors.New("config: worker shutdown must be at most 30s")
	}
	return nil
}

func (cfg Config) validateAddresses() error {
	if (cfg.Service == ServiceAPI || cfg.Service == ServiceChaosLab) && strings.TrimSpace(cfg.HTTPAddr) == "" {
		return errors.New("config: HTTP listen address must not be empty")
	}
	if (cfg.Service == ServiceAPI || cfg.Service == ServiceWorker) && strings.TrimSpace(cfg.OperationalAddr) == "" {
		return errors.New("config: operational HTTP listen address must not be empty")
	}
	if cfg.Service == ServiceAPI || cfg.Service == ServiceWorker {
		return validateDatabaseURL(cfg.DatabaseURL, cfg.Profile, cfg.DatabaseLocalSocket)
	}
	return nil
}

func (cfg Config) validateProduction() error {
	if cfg.Service == ServiceChaosLab {
		return errors.New("config: chaoslab is unavailable in production")
	}
	if cfg.Service == ServiceAPI && !cfg.IngressTLSTerminated {
		return errors.New("config: WDE_INGRESS_TLS_TERMINATED must be true in production")
	}
	if cfg.EnablePprof {
		return errors.New("config: WDE_ENABLE_PPROF must be false in production")
	}
	if cfg.AllowHTTPDestinations {
		return errors.New("config: WDE_ALLOW_HTTP_DESTINATIONS must be false in production")
	}
	return validateProductionSecrets(cfg)
}

func validateDatabaseURL(raw string, profile Profile, localSocket bool) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("config: WDE_DATABASE_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.User.Username() == "" || parsed.Path != "/wde" || parsed.Fragment != "" {
		return errors.New("config: WDE_DATABASE_URL is invalid")
	}
	query := parsed.Query()
	if _, hasPassword := parsed.User.Password(); profile == ProfileProduction && hasPassword {
		return errors.New("config: WDE_DATABASE_URL must not contain a password in production")
	}
	if localSocket {
		if parsed.Host != "" || len(query["host"]) != 1 || query.Get("host") != productionDatabaseSocket ||
			len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" || len(query["passfile"]) != 1 {
			return errors.New("config: local PostgreSQL must use the declared Unix socket")
		}
		if _, err := validateSecretPath("database passfile", query.Get("passfile")); err != nil {
			return err
		}
		return nil
	}
	if parsed.Host == "" || len(query["host"]) != 0 {
		return errors.New("config: WDE_DATABASE_URL is invalid")
	}
	if profile == ProfileProduction && (len(query["sslmode"]) != 1 || query.Get("sslmode") != "verify-full") {
		return errors.New("config: WDE_DATABASE_URL must use sslmode=verify-full in production")
	}
	return nil
}

func validLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
