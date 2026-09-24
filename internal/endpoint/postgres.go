package endpoint

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Create(ctx context.Context, record NewRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, record.WorkspaceID.String()); err != nil {
		return err
	}
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
	return tx.Commit(ctx)
}

func (s *PostgresStore) Get(ctx context.Context, workspaceID, id uuid.UUID) (StoredRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoredRecord{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		return StoredRecord{}, err
	}
	var record StoredRecord
	err = tx.QueryRow(ctx, `SELECT id,workspace_id,status,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version FROM wde.endpoints WHERE workspace_id=$1 AND id=$2`, workspaceID, id).Scan(&record.ID, &record.WorkspaceID, &record.Status, &record.Scheme, &record.Host, &record.Port, &record.Target.FormatVersion, &record.Target.Ciphertext, &record.Target.Nonce, &record.Target.KEKVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredRecord{}, ErrNotFound
	}
	if err != nil {
		return StoredRecord{}, err
	}
	rows, err := tx.Query(ctx, `SELECT event_type FROM wde.endpoint_subscriptions WHERE workspace_id=$1 AND endpoint_id=$2 ORDER BY event_type`, workspaceID, id)
	if err != nil {
		return StoredRecord{}, err
	}
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			rows.Close()
			return StoredRecord{}, err
		}
		record.EventTypes = append(record.EventTypes, eventType)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return StoredRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredRecord{}, err
	}
	return record, nil
}
