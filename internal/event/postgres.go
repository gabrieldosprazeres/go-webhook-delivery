package event

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Publish(ctx context.Context, record NewRecord) (StoreResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoreResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, record.WorkspaceID.String()); err != nil {
		return StoreResult{}, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO wde.events (id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,payload_expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (workspace_id,idempotency_key_hash) DO NOTHING`, record.ID, record.WorkspaceID, record.KeyHash, record.Fingerprint, record.FingerprintVersion, record.EventType, record.Payload.FormatVersion, record.Payload.Ciphertext, record.Payload.Nonce, record.Payload.KEKVersion, record.PayloadSize, record.PayloadExpiresAt)
	if err != nil {
		return StoreResult{}, err
	}
	if command.RowsAffected() == 0 {
		var existingID uuid.UUID
		var existingFingerprint []byte
		var version int16
		err = tx.QueryRow(ctx, `SELECT id,idempotency_fingerprint,fingerprint_version FROM wde.events WHERE workspace_id=$1 AND idempotency_key_hash=$2`, record.WorkspaceID, record.KeyHash).Scan(&existingID, &existingFingerprint, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return StoreResult{}, err
		}
		if err != nil {
			return StoreResult{}, err
		}
		if version != record.FingerprintVersion || len(existingFingerprint) != len(record.Fingerprint) || subtle.ConstantTimeCompare(existingFingerprint, record.Fingerprint) != 1 {
			return StoreResult{}, ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT id FROM wde.deliveries WHERE workspace_id=$1 AND event_id=$2 ORDER BY id`, record.WorkspaceID, existingID)
		if err != nil {
			return StoreResult{}, err
		}
		ids, err := collectIDs(rows)
		if err != nil {
			return StoreResult{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return StoreResult{}, err
		}
		return StoreResult{EventID: existingID, DeliveryIDs: ids, Duplicate: true}, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT e.id FROM wde.endpoints e JOIN wde.endpoint_subscriptions s ON s.workspace_id=e.workspace_id AND s.endpoint_id=e.id WHERE e.workspace_id=$1 AND e.status='active' AND s.event_type=$2 ORDER BY e.id LIMIT 101`, record.WorkspaceID, record.EventType)
	if err != nil {
		return StoreResult{}, err
	}
	endpointIDs, err := collectIDs(rows)
	if err != nil {
		return StoreResult{}, err
	}
	if len(endpointIDs) > 100 {
		return StoreResult{}, ErrInvalid
	}
	deliveryIDs := make([]uuid.UUID, 0, len(endpointIDs))
	for _, endpointID := range endpointIDs {
		deliveryID, err := uuid.NewV7()
		if err != nil {
			return StoreResult{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wde.deliveries (id,workspace_id,event_id,endpoint_id) VALUES ($1,$2,$3,$4)`, deliveryID, record.WorkspaceID, record.ID, endpointID); err != nil {
			return StoreResult{}, err
		}
		deliveryIDs = append(deliveryIDs, deliveryID)
	}
	if err = tx.Commit(ctx); err != nil {
		return StoreResult{}, err
	}
	return StoreResult{EventID: record.ID, DeliveryIDs: deliveryIDs}, nil
}

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Close()
	Err() error
}

func collectIDs(rows rowScanner) ([]uuid.UUID, error) {
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
