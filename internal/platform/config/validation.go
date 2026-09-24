package config

import (
	"errors"
	"net/url"
	"strings"
)

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
		return errors.New("config: WDE_SHUTDOWN_TIMEOUT must be between 1ns and 2m")
	}
	if cfg.DatabaseTimeout <= 0 || cfg.DatabaseTimeout > maxDatabaseTimeout {
		return errors.New("config: WDE_DATABASE_TIMEOUT must be between 1ns and 30s")
	}
	return cfg.validateAddresses()
}

func (cfg Config) validateAddresses() error {
	if (cfg.Service == ServiceAPI || cfg.Service == ServiceChaosLab) && strings.TrimSpace(cfg.HTTPAddr) == "" {
		return errors.New("config: HTTP listen address must not be empty")
	}
	if (cfg.Service == ServiceAPI || cfg.Service == ServiceWorker) && strings.TrimSpace(cfg.OperationalAddr) == "" {
		return errors.New("config: operational HTTP listen address must not be empty")
	}
	if cfg.Service == ServiceAPI || cfg.Service == ServiceWorker {
		return validateDatabaseURL(cfg.DatabaseURL, cfg.Profile)
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

func validateDatabaseURL(raw string, profile Profile) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("config: WDE_DATABASE_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return errors.New("config: WDE_DATABASE_URL is invalid")
	}
	if profile == ProfileProduction && parsed.Query().Get("sslmode") != "verify-full" {
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
