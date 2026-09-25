// Package auth owns API-key issuance, verification and tenant principals.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
)

const dummyToken = "wde_test_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var ErrUnauthorized = errors.New("auth: unauthorized")

type Principal struct {
	APIKeyID    uuid.UUID
	WorkspaceID uuid.UUID
	Scopes      map[string]struct{}
}

type Record struct {
	APIKeyID        uuid.UUID
	WorkspaceID     uuid.UUID
	Verifier        []byte
	Status          string
	WorkspaceStatus string
	ExpiresAt       *time.Time
	Scopes          []string
}

type Lookup interface {
	LookupKey(context.Context, string) (Record, bool, error)
}

type Authenticator struct {
	lookup Lookup
	pepper [32]byte
	dummy  [32]byte
}

type principalKey struct{}

func NewAuthenticator(lookup Lookup, pepper [32]byte) *Authenticator {
	return &Authenticator{lookup: lookup, pepper: pepper, dummy: Verifier(pepper, dummyToken)}
}

func (a *Authenticator) Middleware(scope string, next http.Handler) http.Handler {
	return a.Authenticate(a.Authorize(scope, next))
}

// Authenticate resolves a credential and establishes its principal without
// authorizing a route scope. Quotas can therefore run between both decisions.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _, ok := bearer(r.Header.Get("Authorization"))
		if !ok {
			problem.Write(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		principal, err := a.AuthenticateToken(r.Context(), token)
		if err != nil {
			problem.Write(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// AuthenticateToken exchanges a raw API key for the key's current principal.
// Callers must discard the token immediately and never persist or log it.
func (a *Authenticator) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	prefix, ok := tokenPrefix(token)
	if !ok {
		return Principal{}, ErrUnauthorized
	}
	record, found, err := a.lookup.LookupKey(ctx, prefix)
	candidate := Verifier(a.pepper, token)
	expected := a.dummy[:]
	if found {
		expected = record.Verifier
	}
	valid := len(expected) == sha256.Size && subtle.ConstantTimeCompare(candidate[:], expected) == 1
	if err != nil || !found || !valid || record.Status != "active" || record.WorkspaceStatus != "active" ||
		(record.ExpiresAt != nil && !record.ExpiresAt.After(time.Now())) {
		return Principal{}, ErrUnauthorized
	}
	scopes := make(map[string]struct{}, len(record.Scopes))
	for _, item := range record.Scopes {
		scopes[item] = struct{}{}
	}
	return Principal{APIKeyID: record.APIKeyID, WorkspaceID: record.WorkspaceID, Scopes: scopes}, nil
}

// Authorize checks a scope only after authentication and authenticated quotas.
func (a *Authenticator) Authorize(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			problem.Write(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		if _, allowed := principal.Scopes[scope]; !allowed {
			problem.Write(w, r, http.StatusForbidden, "insufficient_scope", "Insufficient scope")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func bearer(header string) (string, string, bool) {
	if len(header) > 256 || !strings.HasPrefix(header, "Bearer ") {
		return "", "", false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	prefix, ok := tokenPrefix(token)
	return token, prefix, ok
}

func tokenPrefix(token string) (string, bool) {
	if len(token) > 249 {
		return "", false
	}
	parts := strings.SplitN(token, "_", 4)
	if len(parts) != 4 || parts[0] != "wde" || (parts[1] != "test" && parts[1] != "live") || len(parts[2]) != 16 {
		return "", false
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return "", false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil || len(secret) != 32 {
		clear(secret)
		return "", false
	}
	clear(secret)
	return parts[2], true
}

// PresentedPrefix returns a syntactically valid, unauthenticated token prefix.
// It is suitable only for best-effort local throttling, never as identity.
func PresentedPrefix(header string) (string, bool) {
	_, prefix, ok := bearer(header)
	return prefix, ok
}

func Generate(test bool, pepper [32]byte) (token string, prefix string, verifier [32]byte, err error) {
	var prefixRaw [8]byte
	var secret [32]byte
	if _, err = rand.Read(prefixRaw[:]); err != nil {
		return "", "", verifier, errors.New("auth: generate prefix")
	}
	if _, err = rand.Read(secret[:]); err != nil {
		return "", "", verifier, errors.New("auth: generate secret")
	}
	prefix = hex.EncodeToString(prefixRaw[:])
	environment := "live"
	if test {
		environment = "test"
	}
	token = "wde_" + environment + "_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secret[:])
	clear(secret[:])
	return token, prefix, Verifier(pepper, token), nil
}

func Verifier(pepper [32]byte, token string) [32]byte {
	mac := hmac.New(sha256.New, pepper[:])
	_, _ = mac.Write([]byte(token))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
