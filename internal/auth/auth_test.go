package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateAndParse(t *testing.T) {
	var pepper [32]byte
	pepper[0] = 1
	token, prefix, verifier, err := Generate(true, pepper)
	if err != nil {
		t.Fatal(err)
	}
	parsed, parsedPrefix, ok := bearer("Bearer " + token)
	if !ok || parsed != token || parsedPrefix != prefix {
		t.Fatalf("parse failed: %v %q", ok, parsedPrefix)
	}
	if verifier != Verifier(pepper, token) {
		t.Fatal("verifier mismatch")
	}
}

type fixedLookup struct{ record Record }

func (f fixedLookup) LookupKey(context.Context, string) (Record, bool, error) {
	return f.record, true, nil
}

func TestRevokedKeyIsRejected(t *testing.T) {
	var pepper [32]byte
	token, prefix, verifier, err := Generate(true, pepper)
	if err != nil {
		t.Fatal(err)
	}
	lookup := fixedLookup{record: Record{APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Verifier: verifier[:], Status: "revoked", WorkspaceStatus: "active", Scopes: []string{"events:write"}}}
	handler := NewAuthenticator(lookup, pepper).Middleware("events:write", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("revoked key reached handler") }))
	request := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("prefix=%s status=%d", prefix, response.Code)
	}
}

func TestInactiveWorkspaceIsRejected(t *testing.T) {
	var pepper [32]byte
	token, _, verifier, err := Generate(true, pepper)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"suspended", "deleting"} {
		t.Run(status, func(t *testing.T) {
			lookup := fixedLookup{record: Record{APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Verifier: verifier[:], Status: "active", WorkspaceStatus: status, Scopes: []string{"events:write"}}}
			called := false
			handler := NewAuthenticator(lookup, pepper).Middleware("events:write", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			request := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if called || response.Code != http.StatusUnauthorized {
				t.Fatalf("called=%v status=%d", called, response.Code)
			}
		})
	}
}

func TestInsufficientScopeIsForbidden(t *testing.T) {
	var pepper [32]byte
	token, _, verifier, err := Generate(true, pepper)
	if err != nil {
		t.Fatal(err)
	}
	lookup := fixedLookup{record: Record{APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Verifier: verifier[:], Status: "active", WorkspaceStatus: "active", Scopes: []string{"deliveries:read"}}}
	handler := NewAuthenticator(lookup, pepper).Middleware("events:write", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("insufficient scope reached handler") }))
	request := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}

type missingLookup struct{ calls int }

func (m *missingLookup) LookupKey(context.Context, string) (Record, bool, error) {
	m.calls++
	return Record{}, false, nil
}

func TestUnknownPrefixUsesGenericAuthenticationFailure(t *testing.T) {
	var pepper [32]byte
	token, _, _, err := Generate(true, pepper)
	if err != nil {
		t.Fatal(err)
	}
	lookup := &missingLookup{}
	handler := NewAuthenticator(lookup, pepper).Middleware("events:write", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unknown key reached handler") }))
	request := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if lookup.calls != 1 || response.Code != http.StatusUnauthorized {
		t.Fatalf("calls=%d status=%d", lookup.calls, response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestBearerRejectsMalformed(t *testing.T) {
	for _, value := range []string{"", "bearer token", "Bearer wde_test_bad_x", "Bearer wde_live_0000000000000000_bad"} {
		if _, _, ok := bearer(value); ok {
			t.Fatalf("accepted %q", value)
		}
	}
}
