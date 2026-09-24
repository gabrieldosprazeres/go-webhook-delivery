package endpoint

import (
	"context"
	"errors"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Create(ctx context.Context, record NewRecord) error {
	return tenanttx.Within(ctx, s.pool, record.WorkspaceID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO wde.endpoints (id,workspace_id,status,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, record.ID, record.WorkspaceID, record.Status, record.Scheme, record.Host, record.Port, record.Target.FormatVersion, record.Target.Ciphertext, record.Target.Nonce, record.Target.KEKVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wde.endpoint_runtime (workspace_id,endpoint_id) VALUES ($1,$2)`, record.WorkspaceID, record.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wde.endpoint_secret_versions (id,workspace_id,endpoint_id,key_id,cipher_format_version,secret_ciphertext,secret_nonce,kek_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, record.SecretVersionID, record.WorkspaceID, record.ID, record.KeyID, record.Secret.FormatVersion, record.Secret.Ciphertext, record.Secret.Nonce, record.Secret.KEKVersion); err != nil {
			return err
		}
		for _, eventType := range record.EventTypes {
			if _, err := tx.Exec(ctx, `INSERT INTO wde.endpoint_subscriptions (workspace_id,endpoint_id,event_type) VALUES ($1,$2,$3)`, record.WorkspaceID, record.ID, eventType); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `SELECT wde.append_audit_event($1,$2,$3,$4,'endpoint.create','endpoint',$5,$6,'accepted',NULL)`,
			record.AuditID, record.WorkspaceID, record.ActorType, record.ActorID, record.ID.String(), record.RequestID)
		return err
	})
}

func (s *PostgresStore) Rotate(ctx context.Context, record RotationRecord) (RotationStored, error) {
	var stored RotationStored
	err := tenanttx.Within(ctx, s.pool, record.WorkspaceID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT secret_version_id,key_id,cipher_format_version,
			secret_ciphertext,secret_nonce,kek_version,duplicate
			FROM wde.rotate_endpoint_secret($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			record.CommandID, record.AuditID, record.WorkspaceID, record.EndpointID,
			record.SecretVersionID, record.KeyID, record.Secret.FormatVersion, record.Secret.Ciphertext,
			record.Secret.Nonce, record.Secret.KEKVersion, record.ActorID, record.RequestID,
			record.IdempotencyHash, record.Fingerprint, record.OverlapSeconds).Scan(&stored.SecretVersionID,
			&stored.KeyID, &stored.Secret.FormatVersion, &stored.Secret.Ciphertext, &stored.Secret.Nonce,
			&stored.Secret.KEKVersion, &stored.Duplicate)
		if err == nil {
			return nil
		}
		message := err.Error()
		switch {
		case strings.Contains(message, "rotation_not_found"):
			return ErrNotFound
		case strings.Contains(message, "rotation_conflict"):
			return ErrRotationConflict
		case strings.Contains(message, "rotation_result_expired"):
			return ErrRotationExpired
		case strings.Contains(message, "rotation_in_progress"):
			return ErrRotationInProgress
		case strings.Contains(message, "rotation_invalid"), strings.Contains(message, "rotation_no_active_secret"):
			return ErrInvalid
		default:
			return err
		}
	})
	return stored, err
}

func (s *PostgresStore) Get(ctx context.Context, workspaceID, id uuid.UUID) (StoredRecord, error) {
	var record StoredRecord
	err := tenanttx.Within(ctx, s.pool, workspaceID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,workspace_id,status,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version FROM wde.endpoints WHERE workspace_id=$1 AND id=$2`, workspaceID, id).Scan(&record.ID, &record.WorkspaceID, &record.Status, &record.Scheme, &record.Host, &record.Port, &record.Target.FormatVersion, &record.Target.Ciphertext, &record.Target.Nonce, &record.Target.KEKVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT event_type FROM wde.endpoint_subscriptions WHERE workspace_id=$1 AND endpoint_id=$2 ORDER BY event_type`, workspaceID, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var eventType string
			if err := rows.Scan(&eventType); err != nil {
				return err
			}
			record.EventTypes = append(record.EventTypes, eventType)
		}
		return rows.Err()
	})
	if err != nil {
		return StoredRecord{}, err
	}
	return record, nil
}
