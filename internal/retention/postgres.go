package retention

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

func (s *PostgresStore) Purge(ctx context.Context, batch int) (Counts, error) {
	var result Counts
	if err := s.pool.QueryRow(ctx, `SELECT wde.purge_expired_payloads($1)`, batch).Scan(&result.Payloads); err != nil {
		return Counts{}, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT wde.purge_retired_secrets($1)`, batch).Scan(&result.Secrets); err != nil {
		return Counts{}, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT wde.purge_console_sessions($1)`, batch).Scan(&result.Sessions); err != nil {
		return Counts{}, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT * FROM wde.purge_expired_metadata($1)`, batch).Scan(
		&result.Attempts, &result.Replays, &result.Rotations, &result.Deliveries,
		&result.Events, &result.Audits, &result.Buckets); err != nil {
		return Counts{}, err
	}
	var workspaceID uuid.UUID
	var completed bool
	var checkpoint string
	err := s.pool.QueryRow(ctx, `SELECT * FROM wde.purge_workspace($1)`, batch).Scan(
		&workspaceID, &result.WorkspaceRows, &completed, &checkpoint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Counts{}, err
	}
	if err == nil {
		result.WorkspaceSteps = 1
	}
	return result, nil
}

func (s *PostgresStore) Backlog(ctx context.Context) (Backlog, error) {
	var result Backlog
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `SELECT * FROM wde.retention_backlog()`).Scan(&result.Total, &oldest)
	result.OldestExpiredAt = oldest
	return result, err
}

type AdminStore struct{ pool *pgxpool.Pool }

func NewAdminStore(pool *pgxpool.Pool) *AdminStore { return &AdminStore{pool: pool} }

func (s *AdminStore) QuarantineRestore(ctx context.Context, generation int64) (bool, error) {
	var changed bool
	err := s.pool.QueryRow(ctx, `SELECT wde.enter_restore_quarantine($1)`, generation).Scan(&changed)
	return changed, err
}

func (s *AdminStore) CompleteRestore(ctx context.Context, generation int64) (bool, error) {
	var changed bool
	err := s.pool.QueryRow(ctx, `SELECT wde.complete_restore_reconciliation($1)`, generation).Scan(&changed)
	return changed, err
}

func (s *AdminStore) RequestWorkspacePurge(ctx context.Context, workspaceID uuid.UUID, requestID string) (bool, error) {
	var changed bool
	err := s.pool.QueryRow(ctx, `SELECT wde.request_workspace_purge($1,$2)`, workspaceID, requestID).Scan(&changed)
	return changed, err
}
