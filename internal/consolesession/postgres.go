package consolesession

import (
	"context"
	"errors"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Create(ctx context.Context, principal auth.Principal, prefix string, verifier []byte) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	err = s.pool.QueryRow(ctx, `SELECT session_id FROM wde.create_console_session($1,$2,$3,$4,$5,$6)`,
		id, principal.WorkspaceID, principal.APIKeyID, prefix, verifier, auditID).Scan(&id)
	if err == nil {
		return id, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.Contains(pgErr.Message, "console_session_limit") {
		return uuid.Nil, ErrLimit
	}
	return uuid.Nil, err
}

func (s *PostgresStore) Lookup(ctx context.Context, prefix string) (LookupRecord, bool, error) {
	var record LookupRecord
	err := s.pool.QueryRow(ctx, `SELECT session_id,token_verifier FROM wde.lookup_console_session($1)`, prefix).
		Scan(&record.ID, &record.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return LookupRecord{}, false, nil
	}
	return record, err == nil, err
}

func (s *PostgresStore) Resume(ctx context.Context, sessionID uuid.UUID) (auth.Principal, error) {
	var principal auth.Principal
	var scopes []string
	err := s.pool.QueryRow(ctx, `SELECT api_key_id,workspace_id,scopes FROM wde.resume_console_session($1)`, sessionID).
		Scan(&principal.APIKeyID, &principal.WorkspaceID, &scopes)
	if err != nil {
		return auth.Principal{}, err
	}
	principal.Scopes = make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		principal.Scopes[scope] = struct{}{}
	}
	return principal, nil
}

func (s *PostgresStore) Revoke(ctx context.Context, sessionID uuid.UUID) error {
	auditID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `SELECT wde.revoke_console_session($1,$2,'user_logout')`, sessionID, auditID)
	return err
}
