package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAPIURL(t *testing.T) {
	url, origin, err := validateAPIURL("https://api.example.test")
	if err != nil || url != "https://api.example.test" || origin != url {
		t.Fatalf("validateAPIURL()=(%q,%q,%v)", url, origin, err)
	}
	for _, invalid := range []string{"", "http://api.example.test", "https://user@api.example.test", "https://API.example.test", "https://api.example.test/path", "https://api.example.test?x=1", "https://api.example.test;script-src"} {
		if _, _, err = validateAPIURL(invalid); err == nil {
			t.Fatalf("validateAPIURL(%q) accepted", invalid)
		}
	}
}

func TestProductionSpecReplacesExactlyOneLocalServer(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "openapi.yaml")
	input := "openapi: 3.1.0\nservers:\n" + localServerLine + "\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := loadProductionSpec(path, "https://api.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "localhost") || !strings.Contains(string(result), "https://api.example.test") {
		t.Fatalf("unexpected spec: %s", result)
	}
	if err = os.WriteFile(path, []byte("openapi: 3.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadProductionSpec(path, "https://api.example.test"); err == nil {
		t.Fatal("missing server line accepted")
	}
}

func TestDocsHeadersSpecAndLiveness(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("Swagger UI"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := routes(directory, []byte("openapi: 3.1.0\n"), "https://api.example.test")
	for path, expected := range map[string]string{"/": "Swagger UI", "/openapi.yaml": "openapi: 3.1.0", "/livez": "ok"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Strict-Transport-Security") == "" || response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s missing security headers", path)
		}
	}
}
