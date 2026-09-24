package endpoint

import (
	"context"
	"errors"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("endpoint: invalid input")
	ErrNotFound    = errors.New("endpoint: not found")
	ErrUnavailable = errors.New("endpoint: creation unavailable until outbound SSRF controls are enabled")
)

type CreateInput struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
}

type Created struct {
	Endpoint      Endpoint      `json:"endpoint"`
	SigningSecret SigningSecret `json:"signing_secret"`
}

type SigningSecret struct {
	KeyID  string `json:"key_id"`
	Secret string `json:"secret"`
}

type Endpoint struct {
	ID         uuid.UUID `json:"id"`
	URL        string    `json:"url"`
	Status     string    `json:"status"`
	EventTypes []string  `json:"event_types"`
}

type NewRecord struct {
	ID, WorkspaceID, SecretVersionID uuid.UUID
	AuditID                          uuid.UUID
	Status, Scheme, Host, KeyID      string
	ActorType, ActorID, RequestID    string
	Port                             int
	Target, Secret                   cryptobox.Envelope
	EventTypes                       []string
}

type StoredRecord struct {
	ID, WorkspaceID      uuid.UUID
	Status, Scheme, Host string
	Port                 int
	Target               cryptobox.Envelope
	EventTypes           []string
}

type Store interface {
	Create(context.Context, NewRecord) error
	Get(context.Context, uuid.UUID, uuid.UUID) (StoredRecord, error)
}

type destination struct {
	scheme, host, path string
	port               int
}
