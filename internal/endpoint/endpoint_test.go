package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type memoryStore struct{ record NewRecord }

func (m *memoryStore) Create(_ context.Context, record NewRecord) error {
	m.record = record
	return nil
}
func (m *memoryStore) Get(_ context.Context, workspaceID, id uuid.UUID) (StoredRecord, error) {
	r := m.record
	return StoredRecord{ID: id, WorkspaceID: workspaceID, Status: r.Status, Scheme: r.Scheme, Host: r.Host, Port: r.Port, Target: r.Target, EventTypes: r.EventTypes}, nil
}

func TestCreateRevealsSecretOnceAndGetDecryptsURL(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := &memoryStore{}
	service := NewService(store, config.ProfileTest, true, materials)
	workspaceID := uuid.New()
	created, err := service.Create(context.Background(), workspaceID, CreateInput{URL: "http://127.0.0.1:8081/success?mode=ok", EventTypes: []string{"invoice.created"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.SigningSecret.Secret) != 43 {
		t.Fatalf("secret length=%d", len(created.SigningSecret.Secret))
	}
	got, err := service.Get(context.Background(), workspaceID, created.Endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "http://127.0.0.1:8081/success?mode=ok" {
		t.Fatalf("url=%q", got.URL)
	}
}

func TestRejectsNonLoopbackAndProduction(t *testing.T) {
	if _, _, _, _, err := validateURL(config.ProfileLocal, true, "http://example.com/hook"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, _, err := validateURL(config.ProfileProduction, false, "https://example.com/hook"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPDestinationRequiresExplicitFlag(t *testing.T) {
	if _, _, _, _, err := validateURL(config.ProfileLocal, false, "http://127.0.0.1:8081/hook"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	if _, _, _, _, err := validateURL(config.ProfileLocal, true, "http://127.0.0.1:8081/hook"); err != nil {
		t.Fatalf("err=%v", err)
	}
}
