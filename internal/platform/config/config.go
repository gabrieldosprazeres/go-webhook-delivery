// Package config loads and validates process configuration.
package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultShutdownTimeout = 30 * time.Second
	maxShutdownTimeout     = 2 * time.Minute
	defaultDatabaseTimeout = 5 * time.Second
	maxDatabaseTimeout     = 30 * time.Second
	secretFileMaxBytes     = 64 << 10
	keyMaterialBytes       = 32
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

	cfg := Config{
		Service:         opts.Service,
		Profile:         ProfileLocal,
		Version:         "dev",
		LogLevel:        "info",
		DatabaseTimeout: defaultDatabaseTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	switch opts.Service {
	case ServiceAPI:
		cfg.HTTPAddr = ":8080"
		cfg.OperationalAddr = "127.0.0.1:9090"
	case ServiceChaosLab:
		cfg.HTTPAddr = "127.0.0.1:8081"
	case ServiceWorker:
		cfg.OperationalAddr = "127.0.0.1:9091"
	default:
		return Config{}, errors.New("config: unknown service")
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

func applyEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	if value, ok := lookup("WDE_PROFILE"); ok {
		cfg.Profile = Profile(value)
	}
	if value, ok := lookup("WDE_VERSION"); ok {
		cfg.Version = value
	}
	if value, ok := lookup("WDE_LOG_LEVEL"); ok {
		cfg.LogLevel = value
	}
	if value, ok := lookup("WDE_DATABASE_URL"); ok {
		cfg.DatabaseURL = value
	}
	if value, ok := lookup(httpAddrVariable(cfg.Service)); ok {
		cfg.HTTPAddr = value
	}
	if value, ok := lookup(operationalAddrVariable(cfg.Service)); ok {
		cfg.OperationalAddr = value
	}

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

	cfg.Secrets.AuthPepper, _ = envValue(lookup, "WDE_AUTH_PEPPER_FILE")
	cfg.Secrets.IdempotencyPepper, _ = envValue(lookup, "WDE_IDEMPOTENCY_PEPPER_FILE")
	cfg.Secrets.PayloadKeyring, _ = envValue(lookup, "WDE_PAYLOAD_KEYRING_FILE")
	cfg.Secrets.SigningKeyring, _ = envValue(lookup, "WDE_SIGNING_KEYRING_FILE")

	return nil
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

	cfg.Profile = Profile(*profile)
	cfg.HTTPAddr = *httpAddr
	cfg.OperationalAddr = *operationalAddr
	cfg.ShutdownTimeout = *shutdownTimeout
	cfg.DatabaseTimeout = *databaseTimeout
	cfg.LogLevel = *logLevel
	cfg.Version = *version
	cfg.IngressTLSTerminated = *ingressTLS
	cfg.EnablePprof = *pprof
	cfg.AllowHTTPDestinations = *allowHTTP

	return nil
}

// Validate applies process- and profile-specific fail-closed rules.
func (cfg Config) Validate() error {
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

	if cfg.Service == ServiceAPI || cfg.Service == ServiceChaosLab {
		if strings.TrimSpace(cfg.HTTPAddr) == "" {
			return errors.New("config: HTTP listen address must not be empty")
		}
	}
	if cfg.Service == ServiceAPI || cfg.Service == ServiceWorker {
		if strings.TrimSpace(cfg.OperationalAddr) == "" {
			return errors.New("config: operational HTTP listen address must not be empty")
		}
	}
	if cfg.Service == ServiceAPI || cfg.Service == ServiceWorker {
		if err := validateDatabaseURL(cfg.DatabaseURL, cfg.Profile); err != nil {
			return err
		}
	}

	if cfg.Profile != ProfileProduction {
		return nil
	}
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

	if err := validateProductionSecrets(cfg); err != nil {
		return err
	}

	return nil
}

func validateProductionSecrets(cfg Config) error {
	if cfg.Service == ServiceAPI {
		authPepper, err := validatePepperFile("WDE_AUTH_PEPPER_FILE", cfg.Secrets.AuthPepper)
		if err != nil {
			return err
		}
		idempotencyPepper, err := validatePepperFile("WDE_IDEMPOTENCY_PEPPER_FILE", cfg.Secrets.IdempotencyPepper)
		if err != nil {
			return err
		}
		if authPepper == idempotencyPepper {
			return errors.New("config: authentication and idempotency peppers must use distinct key material")
		}
	}

	payloadKeys, err := validateKeyringFile("WDE_PAYLOAD_KEYRING_FILE", cfg.Secrets.PayloadKeyring)
	if err != nil {
		return err
	}
	signingKeys, err := validateKeyringFile("WDE_SIGNING_KEYRING_FILE", cfg.Secrets.SigningKeyring)
	if err != nil {
		return err
	}
	for material := range payloadKeys {
		if _, reused := signingKeys[material]; reused {
			return errors.New("config: payload and signing keyrings must use distinct key material")
		}
	}
	return nil
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

func readSecretFile(name, path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("config: %s is required in production", name)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("config: %s must reference a regular file, not a symlink", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("config: %s permissions must be at most 0600", name)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("config: %s must be owned by the process user", name)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("config: %s changed while being opened", name)
	}
	contents, err := io.ReadAll(io.LimitReader(file, secretFileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("config: %s cannot be read", name)
	}
	if len(contents) == 0 || len(contents) > secretFileMaxBytes {
		return nil, fmt.Errorf("config: %s has an invalid size", name)
	}
	return contents, nil
}

func validatePepperFile(name, path string) ([keyMaterialBytes]byte, error) {
	var material [keyMaterialBytes]byte
	contents, err := readSecretFile(name, path)
	if err != nil {
		return material, err
	}
	defer clear(contents)

	const prefix = "v1:"
	encoded := strings.TrimSpace(string(contents))
	if !strings.HasPrefix(encoded, prefix) {
		return material, fmt.Errorf("config: %s must use the v1 pepper format", name)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(encoded, prefix))
	if err != nil || len(decoded) != keyMaterialBytes {
		clear(decoded)
		return material, fmt.Errorf("config: %s must contain exactly 32 bytes of base64url key material", name)
	}
	copy(material[:], decoded)
	clear(decoded)
	return material, nil
}

type keyringDocument struct {
	FormatVersion     int                  `json:"format_version"`
	PrimaryKeyVersion int                  `json:"primary_key_version"`
	Keys              []keyringDocumentKey `json:"keys"`
}

type keyringDocumentKey struct {
	Version  int    `json:"version"`
	Material string `json:"material"`
}

func validateKeyringFile(name, path string) (map[[keyMaterialBytes]byte]struct{}, error) {
	contents, err := readSecretFile(name, path)
	if err != nil {
		return nil, err
	}
	defer clear(contents)

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("config: %s must contain valid keyring JSON", name)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("config: %s must contain one JSON document", name)
	}
	if document.FormatVersion != 1 || document.PrimaryKeyVersion <= 0 || len(document.Keys) == 0 || len(document.Keys) > 8 {
		return nil, fmt.Errorf("config: %s has unsupported keyring metadata", name)
	}

	versions := make(map[int]struct{}, len(document.Keys))
	materials := make(map[[keyMaterialBytes]byte]struct{}, len(document.Keys))
	primaryFound := false
	for _, key := range document.Keys {
		if key.Version <= 0 {
			return nil, fmt.Errorf("config: %s contains an invalid key version", name)
		}
		if _, duplicate := versions[key.Version]; duplicate {
			return nil, fmt.Errorf("config: %s contains a duplicate key version", name)
		}
		versions[key.Version] = struct{}{}
		primaryFound = primaryFound || key.Version == document.PrimaryKeyVersion

		decoded, err := base64.RawURLEncoding.Strict().DecodeString(key.Material)
		if err != nil || len(decoded) != keyMaterialBytes {
			clear(decoded)
			return nil, fmt.Errorf("config: %s keys must contain exactly 32 bytes of base64url key material", name)
		}
		var material [keyMaterialBytes]byte
		copy(material[:], decoded)
		clear(decoded)
		if _, duplicate := materials[material]; duplicate {
			return nil, fmt.Errorf("config: %s contains duplicate key material", name)
		}
		materials[material] = struct{}{}
	}
	if !primaryFound {
		return nil, fmt.Errorf("config: %s primary key version does not exist", name)
	}
	return materials, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
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

func validLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
