package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowcaseRendersConfiguredLinksAndSecurityHeaders(t *testing.T) {
	handler := routes(pageData{
		APIURL:      "https://api.example.test",
		DocsURL:     "https://docs.example.test",
		ConsoleURL:  "https://console.example.test",
		GitHubURL:   "https://github.example.test/project",
		ReleaseURL:  "https://github.example.test/project/releases/v1",
		LinkedInURL: "https://linkedin.example.test/person",
		Version:     "v1.2.3",
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	for _, expected := range []string{"Webhook Delivery Engine", "https://docs.example.test", "v1.2.3"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body does not contain %q", expected)
		}
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("Content-Security-Policy is empty")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options is not nosniff")
	}
	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("Strict-Transport-Security is empty")
	}
}

func TestShowcaseKeepsTerminalAlignedAndOpensExternalLinksInNewTab(t *testing.T) {
	handler := routes(pageData{
		APIURL:      "https://api.example.test",
		DocsURL:     "https://docs.example.test",
		ConsoleURL:  "https://console.example.test",
		GitHubURL:   "https://github.example.test/project",
		ReleaseURL:  "https://github.example.test/project/releases/v1",
		LinkedInURL: "https://linkedin.example.test/person",
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	body := response.Body.String()
	if strings.Contains(body, "transform:rotate(") {
		t.Fatal("terminal must not be rotated")
	}
	if count := strings.Count(body, `target="_blank"`); count != 8 {
		t.Fatalf("external links with target=_blank=%d want=8", count)
	}
	for url, want := range map[string]int{
		"https://api.example.test":                        1,
		"https://docs.example.test":                       2,
		"https://console.example.test":                    1,
		"https://github.example.test/project":             2,
		"https://github.example.test/project/releases/v1": 1,
		"https://linkedin.example.test/person":            1,
	} {
		expected := `href="` + url + `" target="_blank" rel="noopener noreferrer"`
		if count := strings.Count(body, expected); count != want {
			t.Errorf("safe external links for %q=%d want=%d", url, count, want)
		}
	}
	for _, anchor := range []string{
		`<a href="#arquitetura">Arquitetura</a>`,
		`<a href="#engenharia">Engenharia</a>`,
	} {
		if !strings.Contains(body, anchor) {
			t.Errorf("internal link changed unexpectedly: %s", anchor)
		}
	}
}

func TestShowcaseLivenessAndNotFound(t *testing.T) {
	handler := routes(pageData{})
	for path, expected := range map[string]int{"/livez": http.StatusOK, "/missing": http.StatusNotFound} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatalf("%s status=%d want=%d", path, response.Code, expected)
		}
	}
}

func TestHealthcheckAddressUsesLoopbackForWildcard(t *testing.T) {
	for input, expected := range map[string]string{
		":8080":      "127.0.0.1:8080",
		"0.0.0.0:90": "127.0.0.1:90",
		"[::]:91":    "127.0.0.1:91",
	} {
		if actual := healthcheckAddress(input); actual != expected {
			t.Fatalf("healthcheckAddress(%q)=%q want=%q", input, actual, expected)
		}
	}
}
