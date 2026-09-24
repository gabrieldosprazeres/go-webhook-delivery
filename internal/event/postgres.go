package event

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Publish(ctx context.Context, record NewRecord) (StoreResult, error) {
	var result StoreResult
	err := tenanttx.Within(ctx, s.pool, record.WorkspaceID, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `INSERT INTO wde.events (id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,payload_expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (workspace_id,idempotency_key_hash) DO NOTHING`, record.ID, record.WorkspaceID, record.KeyHash, record.Fingerprint, record.FingerprintVersion, record.EventType, record.Payload.FormatVersion, record.Payload.Ciphertext, record.Payload.Nonce, record.Payload.KEKVersion, record.PayloadSize, record.PayloadExpiresAt)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 0 {
			return s.insertDeliveries(ctx, tx, record, &result)
		}
		var existingID uuid.UUID
		var existingFingerprint []byte
		var version int16
		err = tx.QueryRow(ctx, `SELECT id,idempotency_fingerprint,fingerprint_version FROM wde.events WHERE workspace_id=$1 AND idempotency_key_hash=$2`, record.WorkspaceID, record.KeyHash).Scan(&existingID, &existingFingerprint, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err != nil {
			return err
		}
		if version != record.FingerprintVersion || len(existingFingerprint) != len(record.Fingerprint) || subtle.ConstantTimeCompare(existingFingerprint, record.Fingerprint) != 1 {
			return ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT id FROM wde.deliveries WHERE workspace_id=$1 AND event_id=$2 ORDER BY id`, record.WorkspaceID, existingID)
		if err != nil {
			return err
		}
		ids, err := collectIDs(rows)
		if err != nil {
			return err
		}
		result = StoreResult{EventID: existingID, DeliveryIDs: ids, Duplicate: true}
		return nil
	})
	return result, err
}

func (s *PostgresStore) insertDeliveries(ctx context.Context, tx pgx.Tx, record NewRecord, result *StoreResult) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT e.id FROM wde.endpoints e JOIN wde.endpoint_subscriptions s ON s.workspace_id=e.workspace_id AND s.endpoint_id=e.id WHERE e.workspace_id=$1 AND e.status='active' AND s.event_type=$2 ORDER BY e.id LIMIT 101`, record.WorkspaceID, record.EventType)
	if err != nil {
		return err
	}
	endpointIDs, err := collectIDs(rows)
	if err != nil {
		return err
	}
	if len(endpointIDs) > 100 {
		return ErrInvalid
	}
	deliveryIDs := make([]uuid.UUID, 0, len(endpointIDs))
	for _, endpointID := range endpointIDs {
		deliveryID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wde.deliveries (id,workspace_id,event_id,endpoint_id) VALUES ($1,$2,$3,$4)`, deliveryID, record.WorkspaceID, record.ID, endpointID); err != nil {
			return err
		}
		deliveryIDs = append(deliveryIDs, deliveryID)
	}
	*result = StoreResult{EventID: record.ID, DeliveryIDs: deliveryIDs}
	return nil
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
