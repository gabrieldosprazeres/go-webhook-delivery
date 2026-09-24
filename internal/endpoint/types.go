package endpoint

import (
	"context"
	"errors"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

var (
	ErrInvalid            = errors.New("endpoint: invalid input")
	ErrNotFound           = errors.New("endpoint: not found")
	ErrUnavailable        = errors.New("endpoint: creation unavailable until outbound SSRF controls are enabled")
	ErrRotationConflict   = errors.New("endpoint: rotation idempotency conflict")
	ErrRotationInProgress = errors.New("endpoint: rotation already in progress")
	ErrRotationExpired    = errors.New("endpoint: idempotent rotation result expired")
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
	Rotate(context.Context, RotationRecord) (RotationStored, error)
}

type destination struct {
	scheme, host, path string
	port               int
}

type RotationInput struct {
	OverlapSeconds int `json:"overlap_seconds"`
}

type RotationResult struct {
	EndpointID uuid.UUID     `json:"endpoint_id"`
	Secret     SigningSecret `json:"signing_secret"`
	Duplicate  bool          `json:"duplicate"`
}

type RotationRecord struct {
	CommandID, AuditID, WorkspaceID, EndpointID, SecretVersionID uuid.UUID
	KeyID, ActorID, RequestID                                    string
	IdempotencyHash, Fingerprint                                 []byte
	OverlapSeconds                                               int
	Secret                                                       cryptobox.Envelope
}

type RotationStored struct {
	SecretVersionID uuid.UUID
	KeyID           string
	Secret          cryptobox.Envelope
	Duplicate       bool
}
