package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
)

func TestPublicListenerDoesNotExposeProbes(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
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
