// Package config loads and validates process configuration.
package config

import (
	"errors"
	"os"
	"time"
)

const (
	defaultShutdownTimeout = 30 * time.Second
	maxShutdownTimeout     = 2 * time.Minute
	defaultDatabaseTimeout = 5 * time.Second
	maxDatabaseTimeout     = 30 * time.Second
)

// Profile identifies the safety policy used by a process.
type Profile string

const (
	ProfileLocal      Profile = "local"
	ProfileTest       Profile = "test"
	ProfileProduction Profile = "production"
)

// Service identifies one of the repository binaries.
type Service string

const (
	ServiceAPI      Service = "api"
	ServiceWorker   Service = "worker"
	ServiceChaosLab Service = "chaoslab"
)

// Config is immutable after startup and safe to share between components.
type Config struct {
	Service               Service
	Profile               Profile
	Version               string
	LogLevel              string
	HTTPAddr              string
	OperationalAddr       string
	DatabaseURL           string
	DatabaseTimeout       time.Duration
	ShutdownTimeout       time.Duration
	IngressTLSTerminated  bool
	EnablePprof           bool
	AllowHTTPDestinations bool
	Secrets               SecretFiles
}

// SecretFiles contains paths to mounted secret material. It never contains the material itself.
type SecretFiles struct {
	AuthPepper        string
	IdempotencyPepper string
	FingerprintPepper string
	PayloadKeyring    string
	SigningKeyring    string
}

// LoadOptions makes configuration loading deterministic in tests.
type LoadOptions struct {
	Service   Service
	Args      []string
	LookupEnv func(string) (string, bool)
}

// Load reads defaults, environment variables and finally non-secret command-line flags.
func Load(opts LoadOptions) (Config, error) {
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	cfg, err := defaults(opts.Service)
	if err != nil {
		return Config{}, err
	}
	if err := applyEnvironment(&cfg, opts.LookupEnv); err != nil {
		return Config{}, err
	}
	if err := applyFlags(&cfg, opts.Args); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults(service Service) (Config, error) {
	cfg := Config{
		Service: service, Profile: ProfileLocal, Version: "dev", LogLevel: "info",
		DatabaseTimeout: defaultDatabaseTimeout, ShutdownTimeout: defaultShutdownTimeout,
	}
	switch service {
	case ServiceAPI:
		cfg.HTTPAddr, cfg.OperationalAddr = ":8080", "127.0.0.1:9090"
	case ServiceChaosLab:
		cfg.HTTPAddr = "127.0.0.1:8081"
	case ServiceWorker:
		cfg.OperationalAddr = "127.0.0.1:9091"
	default:
		return Config{}, errors.New("config: unknown service")
	}
	return cfg, nil
}
