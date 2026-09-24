package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }
func (s *PostgresStore) Claim(ctx context.Context, workerID, attemptID uuid.UUID, lease time.Duration) (Claim, bool, error) {
	var c Claim
	err := s.pool.QueryRow(ctx, `SELECT workspace_id,delivery_id,event_id,endpoint_id,fencing_token,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version,event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,key_id,secret_version_id,secret_cipher_format_version,secret_ciphertext,secret_nonce,secret_kek_version FROM wde.claim_delivery($1,$2,$3)`, workerID, attemptID, lease).Scan(&c.WorkspaceID, &c.DeliveryID, &c.EventID, &c.EndpointID, &c.FencingToken, &c.Scheme, &c.Host, &c.Port, &c.Target.FormatVersion, &c.Target.Ciphertext, &c.Target.Nonce, &c.Target.KEKVersion, &c.EventType, &c.Payload.FormatVersion, &c.Payload.Ciphertext, &c.Payload.Nonce, &c.Payload.KEKVersion, &c.KeyID, &c.SecretVersionID, &c.Secret.FormatVersion, &c.Secret.Ciphertext, &c.Secret.Nonce, &c.Secret.KEKVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Claim{}, false, nil
	}
	return c, err == nil, err
}
func (s *PostgresStore) Finalize(ctx context.Context, c Claim, workerID uuid.UUID, success bool, status *int16, duration int, category string) (bool, error) {
	var changed bool
	err := s.pool.QueryRow(ctx, `SELECT wde.finalize_delivery($1,$2,$3,$4,$5,$6,$7,$8)`, c.WorkspaceID, c.DeliveryID, workerID, c.FencingToken, success, status, duration, category).Scan(&changed)
	return changed, err
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
