package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

var ErrNotFound = errors.New("delivery: not found")

type Claim struct {
	WorkspaceID, DeliveryID, EventID, EndpointID uuid.UUID
	FencingToken                                 int64
	Scheme, Host                                 string
	Port                                         int
	Target                                       cryptobox.Envelope
	EventType                                    string
	Payload                                      cryptobox.Envelope
	KeyID                                        string
	SecretVersionID                              uuid.UUID
	Secret                                       cryptobox.Envelope
}

type Attempt struct {
	ID            uuid.UUID  `json:"id"`
	Sequence      int        `json:"sequence"`
	State         string     `json:"state"`
	Outcome       *string    `json:"outcome,omitempty"`
	HTTPStatus    *int16     `json:"http_status,omitempty"`
	ErrorCategory *string    `json:"error_category,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type Details struct {
	ID         uuid.UUID `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	EndpointID uuid.UUID `json:"endpoint_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Attempts   []Attempt `json:"attempts"`
}

type Store interface {
	Claim(context.Context, uuid.UUID, uuid.UUID, time.Duration) (Claim, bool, error)
	Finalize(context.Context, Claim, uuid.UUID, bool, *int16, int, string) (bool, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Details, error)
}
