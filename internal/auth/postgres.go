package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresLookup struct{ pool *pgxpool.Pool }

func NewPostgresLookup(pool *pgxpool.Pool) *PostgresLookup { return &PostgresLookup{pool: pool} }

func (s *PostgresLookup) LookupKey(ctx context.Context, prefix string) (Record, bool, error) {
	var record Record
	err := s.pool.QueryRow(ctx, `SELECT api_key_id, workspace_id, verifier, status, workspace_status, expires_at, scopes FROM wde.authenticate_key($1)`, prefix).
		Scan(&record.APIKeyID, &record.WorkspaceID, &record.Verifier, &record.Status, &record.WorkspaceStatus, &record.ExpiresAt, &record.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}
