package config

import (
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesFlagsAfterEnvironment(t *testing.T) {
	env := map[string]string{
		"WDE_PROFILE":          "test",
		"WDE_DATABASE_URL":     "postgres://test:test@localhost:5432/wde?sslmode=disable",
		"WDE_API_HTTP_ADDR":    ":9000",
		"WDE_SHUTDOWN_TIMEOUT": "10s",
	}
	cfg, err := Load(LoadOptions{
		Service: ServiceAPI,
		Args:    []string{"--http-addr=:9100", "--shutdown-timeout=12s"},
		LookupEnv: func(name string) (string, bool) {
			value, ok := env[name]
			return value, ok
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Profile != ProfileTest || cfg.HTTPAddr != ":9100" || cfg.ShutdownTimeout.String() != "12s" {
		t.Fatalf("Load() precedence mismatch: %+v", cfg)
	}
}

func TestLoadRejectsInvalidProfile(t *testing.T) {
	_, err := Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":      "staging",
			"WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable",
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "WDE_PROFILE") {
		t.Fatalf("Load() error = %v, want profile validation error", err)
	}
}

func TestLoadAcceptsLocalAndTestProfiles(t *testing.T) {
	for _, profile := range []Profile{ProfileLocal, ProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			// Arrange
			environment := map[string]string{
				"WDE_PROFILE":      string(profile),
				"WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable",
			}

			// Act
			cfg, err := Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(environment)})

			// Assert
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Profile != profile {
				t.Fatalf("Profile = %q, want %q", cfg.Profile, profile)
			}
		})
	}
}

func TestHTTPDestinationFlagIsLoadedLocallyAndRejectedInProduction(t *testing.T) {
	local, err := Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":                 "local",
			"WDE_DATABASE_URL":            "postgres://test:test@localhost:5432/wde?sslmode=disable",
			"WDE_ALLOW_HTTP_DESTINATIONS": "true",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !local.AllowHTTPDestinations {
		t.Fatal("local HTTP destination flag was not loaded")
	}

	_, err = Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":                 "production",
			"WDE_DATABASE_URL":            "postgres://worker@example.com:5432/wde?sslmode=verify-full",
			"WDE_ALLOW_HTTP_DESTINATIONS": "true",
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "WDE_ALLOW_HTTP_DESTINATIONS") {
		t.Fatalf("Load() error = %v, want production HTTP rejection", err)
	}
}

func TestWorkerReliabilityConfiguration(t *testing.T) {
	base := map[string]string{
		"WDE_PROFILE": "test", "WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable",
		"WDE_WORKER_CONCURRENCY": "12", "WDE_WORKER_CLAIM_BATCH_SIZE": "6",
		"WDE_WORKER_WORKSPACE_BATCH_LIMIT": "3", "WDE_WORKER_ENDPOINT_BATCH_LIMIT": "2",
		"WDE_WORKER_POLL_INTERVAL": "100ms", "WDE_WORKER_CLAIM_TIMEOUT": "50ms",
		"WDE_WORKER_REQUEST_TIMEOUT": "5s",
		"WDE_WORKER_LEASE_TTL":       "20s", "WDE_WORKER_RETRY_BASE": "2s", "WDE_WORKER_RETRY_CAP": "10m",
	}
	cfg, err := Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(base)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerConcurrency != 12 || cfg.WorkerClaimBatchSize != 6 || cfg.WorkerLeaseTTL.String() != "20s" {
		t.Fatalf("worker config=%+v", cfg)
	}

	invalid := maps.Clone(base)
	invalid["WDE_WORKER_LEASE_TTL"] = "14s"
	if _, err = Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(invalid)}); err == nil || !strings.Contains(err.Error(), "LEASE_TTL") {
		t.Fatalf("lease validation error=%v", err)
	}
	invalid = maps.Clone(base)
	invalid["WDE_WORKER_CLAIM_BATCH_SIZE"] = "13"
	if _, err = Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(invalid)}); err == nil || !strings.Contains(err.Error(), "CLAIM_BATCH_SIZE") {
		t.Fatalf("batch validation error=%v", err)
	}
	invalid = maps.Clone(base)
	invalid["WDE_WORKER_CLAIM_TIMEOUT"] = "101ms"
	if _, err = Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(invalid)}); err == nil || !strings.Contains(err.Error(), "CLAIM_TIMEOUT") {
		t.Fatalf("claim timeout validation error=%v", err)
	}
}

func TestAPIQuotaConfigurationIsTypedAndValidated(t *testing.T) {
	base := map[string]string{
		"WDE_PROFILE": "test", "WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable",
		"WDE_QUOTA_REPLAY_GLOBAL": "50", "WDE_QUOTA_REPLAY_WORKSPACE": "7",
		"WDE_QUOTA_REPLAY_API_KEY": "5", "WDE_QUOTA_REPLAY_RESOURCE": "2",
		"WDE_QUOTA_REPLAY_WINDOW": "30s",
	}
	cfg, err := Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(base)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quotas.Replay.Global != 50 || cfg.Quotas.Replay.Workspace != 7 ||
		cfg.Quotas.Replay.APIKey != 5 || cfg.Quotas.Replay.Resource != 2 ||
		cfg.Quotas.Replay.Window != 30*time.Second {
		t.Fatalf("replay quota=%+v", cfg.Quotas.Replay)
	}
	invalid := maps.Clone(base)
	invalid["WDE_QUOTA_REPLAY_API_KEY"] = "8"
	if _, err = Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(invalid)}); err == nil ||
		!strings.Contains(err.Error(), "quota") {
		t.Fatalf("invalid quota error=%v", err)
	}
}

func TestAPIEdgeLimiterConfigurationIsTypedAndValidated(t *testing.T) {
	base := map[string]string{
		"WDE_PROFILE": "test", "WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable",
		"WDE_EDGE_MAX_IN_FLIGHT": "32", "WDE_EDGE_GLOBAL": "500",
		"WDE_EDGE_ORIGIN": "40", "WDE_EDGE_PREFIX": "20",
		"WDE_EDGE_MAX_BUCKETS": "128", "WDE_EDGE_WINDOW": "30s",
	}
	cfg, err := Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(base)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Edge.MaxInFlight != 32 || cfg.Edge.Prefix != 20 || cfg.Edge.Window != 30*time.Second {
		t.Fatalf("edge config=%+v", cfg.Edge)
	}
	invalid := maps.Clone(base)
	invalid["WDE_EDGE_MAX_BUCKETS"] = "4"
	if _, err = Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(invalid)}); err == nil ||
		!strings.Contains(err.Error(), "edge limiter") {
		t.Fatalf("edge validation error=%v", err)
	}
}

func TestProductionRejectsUnsafeDatabaseWithoutLeakingIt(t *testing.T) {
	const databaseURL = "postgres://sensitive-user:top-secret@db.example:5432/wde?sslmode=disable"
	_, err := Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":      "production",
			"WDE_DATABASE_URL": databaseURL,
		}),
	})
	if err == nil {
		t.Fatal("Load() error = nil, want TLS validation error")
	}
	if strings.Contains(err.Error(), "top-secret") || strings.Contains(err.Error(), databaseURL) {
		t.Fatalf("Load() leaked database URL: %v", err)
	}
}

func TestProductionAcceptsExplicitLocalDatabaseSocket(t *testing.T) {
	dir := t.TempDir()
	passfile := filepath.Join(dir, "worker.pgpass")
	if err := os.WriteFile(passfile, []byte("localhost:5432:wde:wde_worker:test"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":               "production",
			"WDE_DATABASE_LOCAL_SOCKET": "true",
			"WDE_DATABASE_URL": "postgres://wde_worker@/wde?host=%2Fvar%2Frun%2Fpostgresql" +
				"&sslmode=disable&passfile=" + url.QueryEscape(passfile),
			"WDE_PAYLOAD_KEYRING_FILE": writeKeyring(t, dir, "payload.json", 1),
			"WDE_SIGNING_KEYRING_FILE": writeKeyring(t, dir, "signing.json", 2),
		}),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DatabaseLocalSocket {
		t.Fatal("DatabaseLocalSocket = false, want true")
	}
}

func TestDatabaseSocketDeclarationFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "wrong directory", url: "postgres://worker@/wde?host=%2Ftmp&sslmode=disable"},
		{name: "tcp override", url: "postgres://worker@db.example/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable"},
		{name: "tls ambiguity", url: "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=require"},
		{name: "duplicate host", url: "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&host=evil&sslmode=disable"},
		{name: "duplicate TLS mode", url: "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable&sslmode=require"},
		{name: "missing passfile", url: "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable"},
		{name: "duplicate passfile", url: "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable&passfile=%2Fa&passfile=%2Fb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDatabaseURL(test.url, ProfileProduction, true)
			if err == nil || !strings.Contains(err.Error(), "declared Unix socket") {
				t.Fatalf("validateDatabaseURL() error = %v", err)
			}
		})
	}
}

func TestProductionDatabaseURLRejectsEmbeddedPasswordAndAmbiguousTarget(t *testing.T) {
	for _, databaseURL := range []string{
		"postgres://worker:secret@db.example/wde?sslmode=verify-full",
		"postgres://worker@db.example/other?sslmode=verify-full",
		"postgres://db.example/wde?sslmode=verify-full",
		"postgres://worker@db.example/wde?sslmode=verify-full&sslmode=disable",
		"postgres://worker@db.example/wde?sslmode=verify-full#fragment",
	} {
		if err := validateDatabaseURL(databaseURL, ProfileProduction, false); err == nil {
			t.Fatalf("validateDatabaseURL(%q) accepted", databaseURL)
		}
	}
}

func TestProductionDatabaseSocketRejectsInsecurePassfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.pgpass")
	if err := os.WriteFile(path, []byte("localhost:5432:wde:wde_worker:test"), 0o644); err != nil {
		t.Fatal(err)
	}
	databaseURL := "postgres://worker@/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable&passfile=" + url.QueryEscape(path)
	err := validateDatabaseURL(databaseURL, ProfileProduction, true)
	if err == nil || !strings.Contains(err.Error(), "permissions") || strings.Contains(err.Error(), path) {
		t.Fatalf("validateDatabaseURL() error = %v", err)
	}
}

func TestProductionTCPRejectsSocketQueryOverride(t *testing.T) {
	err := validateDatabaseURL(
		"postgres://worker@db.example/wde?host=%2Fvar%2Frun%2Fpostgresql&sslmode=verify-full",
		ProfileProduction,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("validateDatabaseURL() error = %v", err)
	}
}

func TestProductionAcceptsVersionedSecretFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(LoadOptions{
		Service: ServiceWorker,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":              "production",
			"WDE_DATABASE_URL":         "postgres://worker@example.com:5432/wde?sslmode=verify-full",
			"WDE_PAYLOAD_KEYRING_FILE": writeKeyring(t, dir, "payload.json", 1),
			"WDE_SIGNING_KEYRING_FILE": writeKeyring(t, dir, "signing.json", 2),
		}),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestProductionAPIAcceptsDistinctVersionedSecretFiles(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	environment := map[string]string{
		"WDE_PROFILE":                 "production",
		"WDE_DATABASE_URL":            "postgres://api@example.com:5432/wde?sslmode=verify-full",
		"WDE_INGRESS_TLS_TERMINATED":  "true",
		"WDE_AUTH_PEPPER_FILE":        writePepper(t, dir, "auth.pepper", 1),
		"WDE_IDEMPOTENCY_PEPPER_FILE": writePepper(t, dir, "idempotency.pepper", 2),
		"WDE_FINGERPRINT_PEPPER_FILE": writePepper(t, dir, "fingerprint.pepper", 3),
		"WDE_RATE_LIMIT_PEPPER_FILE":  writePepper(t, dir, "rate-limit.pepper", 6),
		"WDE_CURSOR_PEPPER_FILE":      writePepper(t, dir, "cursor.pepper", 7),
		"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload.json", 4),
		"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing.json", 5),
	}

	// Act
	cfg, err := Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(environment)})

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Profile != ProfileProduction {
		t.Fatalf("Profile = %q, want %q", cfg.Profile, ProfileProduction)
	}
}

func TestProductionRejectsReusedCryptographicMaterial(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(t *testing.T, dir string) map[string]string
		wantInError string
	}{
		{
			name: "authentication and idempotency peppers",
			configure: func(t *testing.T, dir string) map[string]string {
				sharedPepper := writePepper(t, dir, "shared.pepper", 1)
				return map[string]string{
					"WDE_PROFILE":                 "production",
					"WDE_DATABASE_URL":            "postgres://api@example.com:5432/wde?sslmode=verify-full",
					"WDE_INGRESS_TLS_TERMINATED":  "true",
					"WDE_AUTH_PEPPER_FILE":        sharedPepper,
					"WDE_IDEMPOTENCY_PEPPER_FILE": sharedPepper,
					"WDE_FINGERPRINT_PEPPER_FILE": writePepper(t, dir, "fingerprint.pepper", 2),
					"WDE_RATE_LIMIT_PEPPER_FILE":  writePepper(t, dir, "rate-limit.pepper", 5),
					"WDE_CURSOR_PEPPER_FILE":      writePepper(t, dir, "cursor.pepper", 6),
					"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload.json", 3),
					"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing.json", 4),
				}
			},
			wantInError: "distinct key material",
		},
		{
			name: "authentication and rate limit peppers",
			configure: func(t *testing.T, dir string) map[string]string {
				sharedPepper := writePepper(t, dir, "shared-rate.pepper", 7)
				return map[string]string{
					"WDE_PROFILE":                 "production",
					"WDE_DATABASE_URL":            "postgres://api@example.com:5432/wde?sslmode=verify-full",
					"WDE_INGRESS_TLS_TERMINATED":  "true",
					"WDE_AUTH_PEPPER_FILE":        sharedPepper,
					"WDE_IDEMPOTENCY_PEPPER_FILE": writePepper(t, dir, "idempotency-rate.pepper", 8),
					"WDE_FINGERPRINT_PEPPER_FILE": writePepper(t, dir, "fingerprint-rate.pepper", 9),
					"WDE_RATE_LIMIT_PEPPER_FILE":  sharedPepper,
					"WDE_CURSOR_PEPPER_FILE":      writePepper(t, dir, "cursor-rate.pepper", 12),
					"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload-rate.json", 10),
					"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing-rate.json", 11),
				}
			},
			wantInError: "distinct key material",
		},
		{
			name: "rate limit and cursor peppers",
			configure: func(t *testing.T, dir string) map[string]string {
				sharedPepper := writePepper(t, dir, "shared-cursor.pepper", 13)
				return map[string]string{
					"WDE_PROFILE":                 "production",
					"WDE_DATABASE_URL":            "postgres://api@example.com:5432/wde?sslmode=verify-full",
					"WDE_INGRESS_TLS_TERMINATED":  "true",
					"WDE_AUTH_PEPPER_FILE":        writePepper(t, dir, "auth-cursor.pepper", 14),
					"WDE_IDEMPOTENCY_PEPPER_FILE": writePepper(t, dir, "idempotency-cursor.pepper", 15),
					"WDE_FINGERPRINT_PEPPER_FILE": writePepper(t, dir, "fingerprint-cursor.pepper", 16),
					"WDE_RATE_LIMIT_PEPPER_FILE":  sharedPepper,
					"WDE_CURSOR_PEPPER_FILE":      sharedPepper,
					"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload-cursor.json", 17),
					"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing-cursor.json", 18),
				}
			},
			wantInError: "distinct key material",
		},
		{
			name: "payload and signing keyrings",
			configure: func(t *testing.T, dir string) map[string]string {
				sharedKeyring := writeKeyring(t, dir, "shared.json", 4)
				return map[string]string{
					"WDE_PROFILE":              "production",
					"WDE_DATABASE_URL":         "postgres://worker@example.com:5432/wde?sslmode=verify-full",
					"WDE_PAYLOAD_KEYRING_FILE": sharedKeyring,
					"WDE_SIGNING_KEYRING_FILE": sharedKeyring,
				}
			},
			wantInError: "distinct key material",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			environment := test.configure(t, t.TempDir())

			// Act
			_, err := Load(LoadOptions{Service: serviceForEnvironment(environment), LookupEnv: mapLookup(environment)})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("Load() error = %v, want error containing %q", err, test.wantInError)
			}
		})
	}
}

func TestProductionRejectsSecretSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	cfg := Config{
		Service:           ServiceWorker,
		Profile:           ProfileProduction,
		Version:           "test",
		LogLevel:          "info",
		DatabaseURL:       "postgres://worker@example.com:5432/wde?sslmode=verify-full",
		DatabaseTimeout:   defaultDatabaseTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		OperationalAddr:   "127.0.0.1:9091",
		WorkerConcurrency: 8, WorkerClaimBatchSize: 8,
		WorkerWorkspaceLimit: 2, WorkerEndpointLimit: 2,
		WorkerPollInterval: 250 * time.Millisecond, WorkerClaimTimeout: 200 * time.Millisecond,
		WorkerRequestTimeout: 10 * time.Second,
		WorkerLeaseTTL:       30 * time.Second, WorkerRetryBase: time.Second, WorkerRetryCap: 15 * time.Minute,
		Edge: defaultEdgeConfig(), Quotas: defaultQuotas(), Telemetry: defaultTelemetryConfig(),
		Secrets: SecretFiles{PayloadKeyring: link, SigningKeyring: link},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Validate() error = %v, want symlink rejection", err)
	}
}

func TestProductionRejectsMalformedKeyringContents(t *testing.T) {
	dir := t.TempDir()
	validKeyring := writeKeyring(t, dir, "valid.json", 9)
	tests := []struct {
		name     string
		contents string
	}{
		{name: "empty", contents: ""},
		{name: "arbitrary", contents: "not-a-keyring"},
		{name: "short key", contents: `{"format_version":1,"primary_key_version":1,"keys":[{"version":1,"material":"c2hvcnQ"}]}`},
		{name: "unknown format", contents: `{"format_version":2,"primary_key_version":1,"keys":[{"version":1,"material":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			_, err := Load(LoadOptions{
				Service: ServiceWorker,
				LookupEnv: mapLookup(map[string]string{
					"WDE_PROFILE":              "production",
					"WDE_DATABASE_URL":         "postgres://worker@example.com:5432/wde?sslmode=verify-full",
					"WDE_PAYLOAD_KEYRING_FILE": path,
					"WDE_SIGNING_KEYRING_FILE": validKeyring,
				}),
			})
			if err == nil {
				t.Fatal("Load() error = nil, want malformed secret rejection")
			}
		})
	}
}

func TestProductionRejectsShortPepper(t *testing.T) {
	dir := t.TempDir()
	authPepper := filepath.Join(dir, "auth.pepper")
	if err := os.WriteFile(authPepper, []byte("v1:c2hvcnQ"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := Load(LoadOptions{
		Service: ServiceAPI,
		LookupEnv: mapLookup(map[string]string{
			"WDE_PROFILE":                 "production",
			"WDE_DATABASE_URL":            "postgres://api@example.com:5432/wde?sslmode=verify-full",
			"WDE_INGRESS_TLS_TERMINATED":  "true",
			"WDE_AUTH_PEPPER_FILE":        authPepper,
			"WDE_IDEMPOTENCY_PEPPER_FILE": writePepper(t, dir, "idempotency.pepper", 2),
			"WDE_FINGERPRINT_PEPPER_FILE": writePepper(t, dir, "fingerprint.pepper", 3),
			"WDE_RATE_LIMIT_PEPPER_FILE":  writePepper(t, dir, "rate-limit.pepper", 6),
			"WDE_CURSOR_PEPPER_FILE":      writePepper(t, dir, "cursor.pepper", 7),
			"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload.json", 4),
			"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing.json", 5),
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("Load() error = %v, want pepper length rejection", err)
	}
}

func TestChaosLabDefaultsToLoopback(t *testing.T) {
	cfg, err := Load(LoadOptions{Service: ServiceChaosLab, LookupEnv: mapLookup(nil)})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8081" {
		t.Fatalf("HTTPAddr = %q, want loopback", cfg.HTTPAddr)
	}
}

func TestOperationalListenersDefaultToDistinctLoopbackPorts(t *testing.T) {
	tests := []struct {
		service Service
		want    string
	}{
		{service: ServiceAPI, want: "127.0.0.1:9090"},
		{service: ServiceWorker, want: "127.0.0.1:9091"},
	}
	for _, test := range tests {
		t.Run(string(test.service), func(t *testing.T) {
			values := map[string]string{"WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable"}
			cfg, err := Load(LoadOptions{Service: test.service, LookupEnv: mapLookup(values)})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.OperationalAddr != test.want {
				t.Fatalf("OperationalAddr = %q, want %q", cfg.OperationalAddr, test.want)
			}
		})
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func serviceForEnvironment(environment map[string]string) Service {
	if _, isAPI := environment["WDE_AUTH_PEPPER_FILE"]; isAPI {
		return ServiceAPI
	}
	return ServiceWorker
}

func writePepper(t *testing.T, dir, name string, fill byte) string {
	t.Helper()
	material := make([]byte, keyMaterialBytes)
	for index := range material {
		material[index] = fill
	}
	path := filepath.Join(dir, name)
	contents := "v1:" + base64.RawURLEncoding.EncodeToString(material)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func writeKeyring(t *testing.T, dir, name string, fill byte) string {
	t.Helper()
	material := make([]byte, keyMaterialBytes)
	for index := range material {
		material[index] = fill
	}
	contents := fmt.Sprintf(
		`{"format_version":1,"primary_key_version":1,"keys":[{"version":1,"material":%q}]}`,
		base64.RawURLEncoding.EncodeToString(material),
	)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
