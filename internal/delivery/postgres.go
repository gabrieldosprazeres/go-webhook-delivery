package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultClaimTimeout = 2 * time.Second

type PostgresStore struct {
	pool         *pgxpool.Pool
	claimTimeout time.Duration
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, claimTimeout: defaultClaimTimeout}
}

func (s *PostgresStore) ClaimBatch(ctx context.Context, request ClaimRequest) ([]Claim, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	claimCtx, cancel := context.WithTimeout(ctx, s.claimTimeout)
	defer cancel()
	attemptIDs := make([]uuid.UUID, request.Limit)
	for index := range attemptIDs {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		attemptIDs[index] = id
	}
	rows, err := s.pool.Query(claimCtx, `SELECT workspace_id,delivery_id,event_id,endpoint_id,
		fencing_token,attempt_number,max_attempts,scheme,host_ascii,port,
		target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version,
		event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,
		key_id,secret_version_id,secret_cipher_format_version,secret_ciphertext,secret_nonce,secret_kek_version
		FROM wde.claim_deliveries($1,$2,$3,$4,$5,$6)`, request.WorkerID, attemptIDs,
		request.LeaseTTL, request.Limit, request.WorkspaceLimit, request.EndpointLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]Claim, 0, request.Limit)
	for rows.Next() {
		claim, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func scanClaim(row pgx.Row) (Claim, error) {
	var c Claim
	err := row.Scan(&c.WorkspaceID, &c.DeliveryID, &c.EventID, &c.EndpointID,
		&c.FencingToken, &c.AttemptNumber, &c.MaxAttempts, &c.Scheme, &c.Host, &c.Port,
		&c.Target.FormatVersion, &c.Target.Ciphertext, &c.Target.Nonce, &c.Target.KEKVersion,
		&c.EventType, &c.Payload.FormatVersion, &c.Payload.Ciphertext, &c.Payload.Nonce,
		&c.Payload.KEKVersion, &c.KeyID, &c.SecretVersionID, &c.Secret.FormatVersion,
		&c.Secret.Ciphertext, &c.Secret.Nonce, &c.Secret.KEKVersion)
	return c, err
}

func (s *PostgresStore) Finalize(ctx context.Context, c Claim, workerID uuid.UUID, result Result) (bool, error) {
	result = normalizeResult(result)
	var changed bool
	var retryDelay any
	if result.Disposition == DispositionRetry {
		retryDelay = result.RetryAfter
	}
	err := s.pool.QueryRow(ctx, `SELECT wde.finalize_delivery($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.WorkspaceID, c.DeliveryID, workerID, c.FencingToken, result.Disposition,
		result.HTTPStatus, result.DurationMS, result.Category, retryDelay).Scan(&changed)
	return changed, err
}

func normalizeResult(result Result) Result {
	if result.HTTPStatus == nil || (*result.HTTPStatus >= 100 && *result.HTTPStatus <= 599) {
		return result
	}
	result.HTTPStatus = nil
	result.Disposition = DispositionPermanent
	result.Category = "invalid_http_status"
	result.RetryAfter = 0
	return result
}
func (s *PostgresStore) Get(ctx context.Context, workspaceID, deliveryID uuid.UUID) (Details, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Details{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		return Details{}, err
	}
	var d Details
	err = tx.QueryRow(ctx, `SELECT id,event_id,endpoint_id,status,created_at,updated_at FROM wde.deliveries WHERE workspace_id=$1 AND id=$2`, workspaceID, deliveryID).Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Details{}, ErrNotFound
	}
	if err != nil {
		return Details{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,attempt_sequence,state,outcome,http_status,error_category,started_at,finished_at FROM wde.delivery_attempts WHERE workspace_id=$1 AND delivery_id=$2 ORDER BY attempt_sequence`, workspaceID, deliveryID)
	if err != nil {
		return Details{}, err
	}
	defer rows.Close()
	d.Attempts = []Attempt{}
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.Sequence, &a.State, &a.Outcome, &a.HTTPStatus, &a.ErrorCategory, &a.StartedAt, &a.FinishedAt); err != nil {
			return Details{}, err
		}
		d.Attempts = append(d.Attempts, a)
	}
	if err := rows.Err(); err != nil {
		return Details{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Details{}, err
	}
	return d, nil
}
