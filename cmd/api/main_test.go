package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

func TestPublicIndexIsSafeServiceDiscovery(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	publicRoutes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	var document map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["status"] != "available" || document["authentication"] != "required" || len(document) != 4 {
		t.Fatalf("unexpected discovery document: %#v", document)
	}
}

func TestPublicListenerDoesNotExposeProbes(t *testing.T) {
	for _, path := range []string{"/healthz", "/livez", "/readyz", "/metrics", "/debug/pprof/"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		publicRoutes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s X-Request-ID is empty", path)
		}
	}
}

func TestPublicSecurityHeaders(t *testing.T) {
	for _, test := range []struct {
		name string
		hsts bool
	}{
		{name: "internal HTTP behind ingress", hsts: true},
		{name: "local HTTP", hsts: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/missing", nil)
			publicSecurityHeaders(publicRoutes(), test.hsts).ServeHTTP(response, request)

			expected := map[string]string{
				"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
				"X-Frame-Options":         "DENY",
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "no-referrer",
				"Cache-Control":           "no-store",
			}
			for name, value := range expected {
				if got := response.Header().Get(name); got != value {
					t.Fatalf("%s=%q, want %q", name, got, value)
				}
			}
			gotHSTS := response.Header().Get("Strict-Transport-Security")
			if test.hsts && gotHSTS != "max-age=31536000; includeSubDomains" {
				t.Fatalf("Strict-Transport-Security=%q", gotHSTS)
			}
			if !test.hsts && gotHSTS != "" {
				t.Fatalf("local Strict-Transport-Security=%q", gotHSTS)
			}
		})
	}
}

func TestCredentialStdoutPolicy(t *testing.T) {
	if !canWriteCredentialToStdout(config.ProfileLocal, true) {
		t.Fatal("local TTY should be allowed")
	}
	for _, test := range []struct {
		profile config.Profile
		tty     bool
	}{{config.ProfileLocal, false}, {config.ProfileTest, true}, {config.ProfileProduction, true}} {
		if canWriteCredentialToStdout(test.profile, test.tty) {
			t.Fatalf("profile=%s tty=%v should be denied", test.profile, test.tty)
		}
	}
}

func TestWriteCredentialFileIsExclusiveDurableAndCleanable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	cleanup, err := writeCredentialFile(path, []byte("secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if _, err := writeCredentialFile(path, []byte("other")); err == nil {
		t.Fatal("existing file overwritten")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup stat err=%v", err)
	}
}
