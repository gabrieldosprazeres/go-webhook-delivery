package consolesession

import (
	"context"
	"errors"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/google/uuid"
)

type memoryStore struct {
	record    LookupRecord
	principal auth.Principal
	revokeErr error
}

func (m *memoryStore) Create(_ context.Context, p auth.Principal, _ string, verifier []byte) (uuid.UUID, error) {
	m.record = LookupRecord{ID: uuid.New(), Verifier: append([]byte(nil), verifier...)}
	m.principal = p
	return m.record.ID, nil
}
func (m *memoryStore) Lookup(context.Context, string) (LookupRecord, bool, error) {
	return m.record, true, nil
}
func (m *memoryStore) Resume(context.Context, uuid.UUID) (auth.Principal, error) {
	return m.principal, nil
}
func (m *memoryStore) Revoke(context.Context, uuid.UUID) error { return m.revokeErr }

type authLookup struct{ record auth.Record }

func (l authLookup) LookupKey(context.Context, string) (auth.Record, bool, error) {
	return l.record, true, nil
}

func TestLoginAuthenticateAndCSRF(t *testing.T) {
	authPepper := [32]byte{1}
	token := "wde_test_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	principal := auth.Principal{APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Scopes: map[string]struct{}{"deliveries:read": {}}}
	digest := auth.Verifier(authPepper, token)
	authenticator := auth.NewAuthenticator(authLookup{record: auth.Record{
		APIKeyID: principal.APIKeyID, WorkspaceID: principal.WorkspaceID, Verifier: digest[:],
		Status: "active", WorkspaceStatus: "active", Scopes: []string{"deliveries:read"},
	}}, authPepper)
	service := New(&memoryStore{}, authenticator, [32]byte{2}, [32]byte{3})

	session, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Authenticate(context.Background(), session.Token)
	if err != nil || resumed.Principal.WorkspaceID != principal.WorkspaceID {
		t.Fatalf("unexpected resume: %#v %v", resumed, err)
	}
	csrf := service.CSRF(session.Token)
	if !service.ValidCSRF(session.Token, csrf) || service.ValidCSRF(session.Token, csrf+"x") {
		t.Fatal("csrf verifier mismatch")
	}
}

func TestLoginAlwaysRotatesSessionAndLogoutFailsClosed(t *testing.T) {
	authPepper := [32]byte{1}
	token := "wde_test_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	principal := auth.Principal{APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Scopes: map[string]struct{}{}}
	digest := auth.Verifier(authPepper, token)
	authenticator := auth.NewAuthenticator(authLookup{record: auth.Record{
		APIKeyID: principal.APIKeyID, WorkspaceID: principal.WorkspaceID, Verifier: digest[:],
		Status: "active", WorkspaceStatus: "active",
	}}, authPepper)
	store := &memoryStore{}
	service := New(store, authenticator, [32]byte{2}, [32]byte{3})

	first, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Token == second.Token {
		t.Fatalf("session fixation: first=%s second=%s", first.ID, second.ID)
	}

	revokeErr := errors.New("database unavailable")
	store.revokeErr = revokeErr
	if err := service.Logout(context.Background(), second); !errors.Is(err, revokeErr) {
		t.Fatalf("logout error=%v, want %v", err, revokeErr)
	}
}
