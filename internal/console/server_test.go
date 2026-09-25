package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
)

type consoleTestSessionStore struct {
	record      consolesession.LookupRecord
	principal   auth.Principal
	revokeErr   error
	revokeCalls int
	lookupCalls int
}

func (s *consoleTestSessionStore) Create(_ context.Context, principal auth.Principal, _ string,
	verifier []byte,
) (uuid.UUID, error) {
	s.record = consolesession.LookupRecord{ID: uuid.New(), Verifier: append([]byte(nil), verifier...)}
	s.principal = principal
	return s.record.ID, nil
}

func (s *consoleTestSessionStore) Lookup(context.Context, string) (consolesession.LookupRecord, bool, error) {
	s.lookupCalls++
	return s.record, s.record.ID != uuid.Nil, nil
}

func (s *consoleTestSessionStore) Resume(context.Context, uuid.UUID) (auth.Principal, error) {
	return s.principal, nil
}

func (s *consoleTestSessionStore) Revoke(context.Context, uuid.UUID) error {
	s.revokeCalls++
	return s.revokeErr
}

type consoleTestAuthLookup struct{ record auth.Record }

func (l consoleTestAuthLookup) LookupKey(context.Context, string) (auth.Record, bool, error) {
	return l.record, true, nil
}

type consoleTestLimiter struct{ err error }

func (l consoleTestLimiter) Consume(context.Context, auth.Principal, uuid.UUID, ratelimit.Policy) error {
	return l.err
}

func newConsoleTestSessions(scopes ...string) (*consolesession.Service, *consoleTestSessionStore, string) {
	apiKey := "wde_test_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	authPepper := [32]byte{1}
	digest := auth.Verifier(authPepper, apiKey)
	lookup := consoleTestAuthLookup{record: auth.Record{
		APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Verifier: digest[:], Status: "active",
		WorkspaceStatus: "active", Scopes: scopes,
	}}
	store := &consoleTestSessionStore{}
	return consolesession.New(store, auth.NewAuthenticator(lookup, authPepper), [32]byte{2}, [32]byte{3}), store, apiKey
}

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
		response.Header().Get("Cache-Control") != "no-store, private" ||
		response.Header().Get("Referrer-Policy") != "same-origin" {
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

func TestPublicGuardThrottlesBeforeSessionLookup(t *testing.T) {
	sessions, store, apiKey := newConsoleTestSessions("deliveries:read")
	session, err := sessions.Login(context.Background(), apiKey)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.NewEdge(ratelimit.EdgePolicy{
		MaxInFlight: 1, Global: 1, Origin: 1, Prefix: 1, MaxBuckets: 8, Window: time.Minute,
	}, [32]byte{9})
	server := New(Dependencies{
		Sessions: sessions, Origin: "https://console.example.test", SecureCookies: true,
		PublicGuard: limiter.Middleware,
	})

	serve := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "https://console.example.test/app/events/new", nil)
		request.RemoteAddr = "203.0.113.10:4321"
		request.AddCookie(&http.Cookie{Name: productionCookie, Value: session.Token})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}

	if first := serve(); first.Code != http.StatusForbidden {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if store.lookupCalls != 1 {
		t.Fatalf("first request session lookups=%d", store.lookupCalls)
	}
	if second := serve(); second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if store.lookupCalls != 1 {
		t.Fatalf("throttled request reached session store: lookups=%d", store.lookupCalls)
	}
}

func TestSuccessfulLoginSetsHostOnlySessionCookie(t *testing.T) {
	sessions, _, apiKey := newConsoleTestSessions("deliveries:read")
	server := New(Dependencies{Sessions: sessions, Origin: "https://console.example.test", SecureCookies: true})
	pageRequest := httptest.NewRequest(http.MethodGet, "https://console.example.test/login", nil)
	pageResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(pageResponse, pageRequest)
	loginCookie := pageResponse.Result().Cookies()[0]

	form := url.Values{"csrf_token": {loginCookie.Value}, "api_key": {apiKey}}
	request := httptest.NewRequest(http.MethodPost, "https://console.example.test/session", strings.NewReader(form.Encode()))
	request.Header.Set("Origin", "https://console.example.test")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == productionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.Secure || !sessionCookie.HttpOnly ||
		sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != "/" ||
		sessionCookie.Domain != "" || sessionCookie.MaxAge != 3600 {
		t.Fatalf("unsafe session cookie: %#v", sessionCookie)
	}
}

func TestEveryConsoleMutationRejectsCrossSiteRequestsBeforeSideEffects(t *testing.T) {
	sessions, store, apiKey := newConsoleTestSessions(
		"deliveries:read", "deliveries:retry", "endpoints:write", "events:write")
	session, err := sessions.Login(context.Background(), apiKey)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Dependencies{Sessions: sessions, Origin: "https://console.example.test", SecureCookies: true})
	paths := []string{
		"/session/logout",
		"/app/endpoints",
		"/app/events",
		"/app/deliveries/" + uuid.NewString() + "/replays",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodPost, "https://console.example.test"+path,
			strings.NewReader(url.Values{"csrf_token": {sessions.CSRF(session.Token)}}.Encode()))
		request.Header.Set("Origin", "https://evil.example")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		request.AddCookie(&http.Cookie{Name: productionCookie, Value: session.Token})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if store.revokeCalls != 0 {
		t.Fatalf("cross-site logout reached store: calls=%d", store.revokeCalls)
	}
}

func TestLogoutFailsClosedAndSuccessfulLogoutClearsBrowserState(t *testing.T) {
	sessions, store, apiKey := newConsoleTestSessions()
	session, err := sessions.Login(context.Background(), apiKey)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Dependencies{Sessions: sessions, Origin: "https://console.example.test", SecureCookies: true})
	logout := func() *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {sessions.CSRF(session.Token)}}
		request := httptest.NewRequest(http.MethodPost, "https://console.example.test/session/logout",
			strings.NewReader(form.Encode()))
		request.Header.Set("Origin", "https://console.example.test")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.AddCookie(&http.Cookie{Name: productionCookie, Value: session.Token})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}

	store.revokeErr = errors.New("database unavailable")
	failed := logout()
	if failed.Code != http.StatusServiceUnavailable || failed.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("failed logout status=%d clear=%q", failed.Code, failed.Header().Get("Clear-Site-Data"))
	}
	for _, cookie := range failed.Result().Cookies() {
		if cookie.Name == productionCookie && cookie.MaxAge < 0 {
			t.Fatal("failed logout cleared a still-active session cookie")
		}
	}

	store.revokeErr = nil
	succeeded := logout()
	if succeeded.Code != http.StatusSeeOther ||
		succeeded.Header().Get("Clear-Site-Data") != `"cache", "cookies", "storage"` {
		t.Fatalf("logout status=%d clear=%q", succeeded.Code, succeeded.Header().Get("Clear-Site-Data"))
	}
	var cleared bool
	for _, cookie := range succeeded.Result().Cookies() {
		cleared = cleared || cookie.Name == productionCookie && cookie.MaxAge < 0
	}
	if !cleared {
		t.Fatal("successful logout did not expire the session cookie")
	}
}

func TestConsoleScopeAndQuotaChecksPrecedeHandlers(t *testing.T) {
	sessions, _, apiKey := newConsoleTestSessions("deliveries:read")
	session, err := sessions.Login(context.Background(), apiKey)
	if err != nil {
		t.Fatal(err)
	}
	request := func(server *Server, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://console.example.test"+path, nil)
		r.AddCookie(&http.Cookie{Name: productionCookie, Value: session.Token})
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}

	forbidden := request(New(Dependencies{Sessions: sessions, Origin: "https://console.example.test",
		SecureCookies: true}), "/app/events/new")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("scope status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
	unavailable := request(New(Dependencies{Sessions: sessions, Origin: "https://console.example.test",
		SecureCookies: true, Limiter: consoleTestLimiter{err: errors.New("quota database unavailable")}}), "/app")
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("quota status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
}
