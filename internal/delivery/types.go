package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("delivery: not found")
	ErrInvalidClaimRequest = errors.New("delivery: invalid claim request")
	ErrInvalidList         = errors.New("delivery: invalid list request")
	ErrInvalidReplay       = errors.New("delivery: invalid replay request")
	ErrReplayConflict      = errors.New("delivery: replay idempotency conflict")
	ErrInvalidTransition   = errors.New("delivery: replay transition is not allowed")
	ErrPayloadPurged       = errors.New("delivery: payload was purged")
)

type Claim struct {
	WorkspaceID, DeliveryID, EventID, EndpointID uuid.UUID
	FencingToken                                 int64
	AttemptNumber, MaxAttempts                   int16
	Scheme, Host                                 string
	Port                                         int
	Target                                       cryptobox.Envelope
	EventType                                    string
	Payload                                      cryptobox.Envelope
	KeyID                                        string
	SecretVersionID                              uuid.UUID
	Secret                                       cryptobox.Envelope
	Retiring                                     *ClaimSecret
}

type ClaimSecret struct {
	KeyID     string
	VersionID uuid.UUID
	Envelope  cryptobox.Envelope
}

type Disposition string

const (
	DispositionSuccess   Disposition = "success"
	DispositionRetry     Disposition = "retry"
	DispositionPermanent Disposition = "permanent_failure"
)

type Result struct {
	Disposition Disposition
	HTTPStatus  *int16
	DurationMS  int
	Category    string
	RetryAfter  time.Duration
}

type ClaimRequest struct {
	WorkerID       uuid.UUID
	LeaseTTL       time.Duration
	Limit          int
	WorkspaceLimit int
	EndpointLimit  int
}

func (request ClaimRequest) Validate() error {
	if request.WorkerID == uuid.Nil || request.LeaseTTL < 20*time.Second || request.LeaseTTL > 2*time.Minute ||
		request.Limit < 1 || request.Limit > 100 || request.WorkspaceLimit < 1 ||
		request.WorkspaceLimit > request.Limit || request.EndpointLimit < 1 ||
		request.EndpointLimit > request.Limit {
		return ErrInvalidClaimRequest
	}
	return nil
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

type Summary struct {
	ID         uuid.UUID `json:"id"`
	EventID    uuid.UUID `json:"event_id"`
	EndpointID uuid.UUID `json:"endpoint_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ListResult struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type ReplayResult struct {
	CommandID  uuid.UUID `json:"command_id"`
	DeliveryID uuid.UUID `json:"delivery_id"`
	RunNumber  int       `json:"run_number"`
	Duplicate  bool      `json:"duplicate"`
}

type ReplayCommand struct {
	CommandID, AuditID, WorkspaceID, DeliveryID uuid.UUID
	ActorID, Reason, RequestID                  string
	KeyHash, Fingerprint                        []byte
	FingerprintVersion                          int16
}

type Store interface {
	ClaimBatch(context.Context, ClaimRequest) ([]Claim, error)
	Finalize(context.Context, Claim, uuid.UUID, Result) (bool, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (Details, error)
}

type ViewStore interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (Details, error)
	List(context.Context, uuid.UUID, int, string) (ListResult, error)
}

type ReplayStore interface {
	RequestReplay(context.Context, ReplayCommand) (ReplayResult, error)
}
