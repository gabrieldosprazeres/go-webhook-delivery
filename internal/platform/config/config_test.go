package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
					"WDE_PAYLOAD_KEYRING_FILE":    writeKeyring(t, dir, "payload.json", 3),
					"WDE_SIGNING_KEYRING_FILE":    writeKeyring(t, dir, "signing.json", 4),
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
		Service:         ServiceWorker,
		Profile:         ProfileProduction,
		Version:         "test",
		LogLevel:        "info",
		DatabaseURL:     "postgres://worker@example.com:5432/wde?sslmode=verify-full",
		DatabaseTimeout: defaultDatabaseTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
		OperationalAddr: "127.0.0.1:9091",
		Secrets:         SecretFiles{PayloadKeyring: link, SigningKeyring: link},
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
