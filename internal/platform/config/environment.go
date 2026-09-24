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
	applySecretPaths(cfg, lookup)
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

func applySecretPaths(cfg *Config, lookup func(string) (string, bool)) {
	cfg.Secrets.AuthPepper, _ = envValue(lookup, "WDE_AUTH_PEPPER_FILE")
	cfg.Secrets.IdempotencyPepper, _ = envValue(lookup, "WDE_IDEMPOTENCY_PEPPER_FILE")
	cfg.Secrets.FingerprintPepper, _ = envValue(lookup, "WDE_FINGERPRINT_PEPPER_FILE")
	cfg.Secrets.PayloadKeyring, _ = envValue(lookup, "WDE_PAYLOAD_KEYRING_FILE")
	cfg.Secrets.SigningKeyring, _ = envValue(lookup, "WDE_SIGNING_KEYRING_FILE")
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
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("config: parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("config: unexpected positional argument")
	}
	applyParsedFlags(cfg, *profile, *httpAddr, *operationalAddr, *logLevel, *version)
	cfg.ShutdownTimeout, cfg.DatabaseTimeout = *shutdownTimeout, *databaseTimeout
	cfg.IngressTLSTerminated, cfg.EnablePprof, cfg.AllowHTTPDestinations = *ingressTLS, *pprof, *allowHTTP
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
