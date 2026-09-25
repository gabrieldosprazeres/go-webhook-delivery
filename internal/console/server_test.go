package console

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoginPageSetsStrictBrowserProtections(t *testing.T) {
	server := New(Dependencies{Origin: "https://console.example.test", SecureCookies: true})
	request := httptest.NewRequest(http.MethodGet, "https://console.example.test/login", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") ||
		!strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
	if response.Header().Get("Strict-Transport-Security") == "" ||
		response.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("production browser security headers are incomplete")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != loginCSRFCookie || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unsafe login CSRF cookie: %#v", cookie)
	}
}

func TestLoginRejectsCrossOriginBeforeCredentialLookup(t *testing.T) {
	server := New(Dependencies{Origin: "https://console.example.test", SecureCookies: true})
	form := url.Values{"csrf_token": {"invalid"}, "api_key": {"wde_secret"}}
	request := httptest.NewRequest(http.MethodPost, "https://console.example.test/session", strings.NewReader(form.Encode()))
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestLoginRouteUsesConfiguredGuard(t *testing.T) {
	called := false
	guard := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusTooManyRequests)
		})
	}
	server := New(Dependencies{Origin: "http://127.0.0.1:8082", LoginGuard: guard})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8082/session", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if !called || response.Code != http.StatusTooManyRequests {
		t.Fatalf("guard called=%v status=%d", called, response.Code)
	}
}
