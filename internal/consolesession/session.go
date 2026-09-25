// Package consolesession owns short-lived browser sessions derived from API keys.
package consolesession

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized = errors.New("console session: unauthorized")
	ErrLimit        = errors.New("console session: active session limit reached")
)

const dummyToken = "wds_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type LookupRecord struct {
	ID       uuid.UUID
	Verifier []byte
}

type Store interface {
	Create(context.Context, auth.Principal, string, []byte) (uuid.UUID, error)
	Lookup(context.Context, string) (LookupRecord, bool, error)
	Resume(context.Context, uuid.UUID) (auth.Principal, error)
	Revoke(context.Context, uuid.UUID) error
}

type Session struct {
	ID        uuid.UUID
	Token     string
	Principal auth.Principal
}

type Service struct {
	store         Store
	authenticator *auth.Authenticator
	pepper        [32]byte
	csrfPepper    [32]byte
	dummy         [32]byte
}

func New(store Store, authenticator *auth.Authenticator, pepper, csrfPepper [32]byte) *Service {
	return &Service{
		store: store, authenticator: authenticator, pepper: pepper, csrfPepper: csrfPepper,
		dummy: verifier(pepper, dummyToken),
	}
}

func (s *Service) Login(ctx context.Context, apiKey string) (Session, error) {
	principal, err := s.authenticator.AuthenticateToken(ctx, strings.TrimSpace(apiKey))
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	token, prefix, err := generateToken()
	if err != nil {
		return Session{}, err
	}
	digest := verifier(s.pepper, token)
	id, err := s.store.Create(ctx, principal, prefix, digest[:])
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, Token: token, Principal: principal}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	prefix, ok := parseToken(token)
	if !ok {
		return Session{}, ErrUnauthorized
	}
	record, found, err := s.store.Lookup(ctx, prefix)
	candidate := verifier(s.pepper, token)
	expected := s.dummy[:]
	if found {
		expected = record.Verifier
	}
	valid := len(expected) == sha256.Size && subtle.ConstantTimeCompare(candidate[:], expected) == 1
	if err != nil || !found || !valid {
		return Session{}, ErrUnauthorized
	}
	principal, err := s.store.Resume(ctx, record.ID)
	if err != nil {
		return Session{}, ErrUnauthorized
	}
	return Session{ID: record.ID, Token: token, Principal: principal}, nil
}

func (s *Service) Logout(ctx context.Context, session Session) error {
	return s.store.Revoke(ctx, session.ID)
}

func (s *Service) CSRF(token string) string {
	mac := hmac.New(sha256.New, s.csrfPepper[:])
	_, _ = mac.Write([]byte("csrf:v1\n"))
	_, _ = mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) ValidCSRF(token, supplied string) bool {
	expected := s.CSRF(token)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) == 1
}

func generateToken() (string, string, error) {
	var prefixRaw [8]byte
	var secret [32]byte
	if _, err := rand.Read(prefixRaw[:]); err != nil {
		return "", "", errors.New("console session: generate prefix")
	}
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", errors.New("console session: generate secret")
	}
	prefix := hex.EncodeToString(prefixRaw[:])
	token := "wds_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secret[:])
	clear(secret[:])
	return token, prefix, nil
}

func parseToken(token string) (string, bool) {
	if len(token) > 128 {
		return "", false
	}
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != "wds" || len(parts[1]) != 16 {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(secret) != 32 {
		clear(secret)
		return "", false
	}
	clear(secret)
	return parts[1], true
}

func verifier(pepper [32]byte, token string) [32]byte {
	mac := hmac.New(sha256.New, pepper[:])
	_, _ = mac.Write([]byte(token))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
