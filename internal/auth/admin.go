package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAlreadyBootstrapped = errors.New("auth: workspace already exists")
var ErrCredentialSink = errors.New("auth: credential sink is required")

type BootstrapResult struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	APIKeyID    uuid.UUID `json:"api_key_id"`
	Token       string    `json:"api_key"`
}

type AdminStore struct {
	pool   *pgxpool.Pool
	pepper [32]byte
	test   bool
}

func NewAdminStore(pool *pgxpool.Pool, pepper [32]byte, test bool) *AdminStore {
	return &AdminStore{pool: pool, pepper: pepper, test: test}
}

type CredentialSink func(BootstrapResult) (cleanup func(), err error)

func (s *AdminStore) Bootstrap(ctx context.Context, name string, sink CredentialSink) (BootstrapResult, error) {
	if len(name) < 1 || len(name) > 120 || sink == nil {
		return BootstrapResult{}, ErrCredentialSink
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x5744455f424f4f54)); err != nil {
		return BootstrapResult{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wde.workspaces`).Scan(&count); err != nil {
		return BootstrapResult{}, err
	}
	if count != 0 {
		return BootstrapResult{}, ErrAlreadyBootstrapped
	}
	workspaceID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, err
	}
	keyID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, err
	}
	token, prefix, verifier, err := Generate(s.test, s.pepper)
	if err != nil {
		return BootstrapResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wde.workspaces (id,name) VALUES ($1,$2)`, workspaceID, name); err != nil {
		return BootstrapResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wde.api_keys (id,workspace_id,prefix,verifier) VALUES ($1,$2,$3,$4)`, keyID, workspaceID, prefix, verifier[:]); err != nil {
		return BootstrapResult{}, err
	}
	for _, scope := range []string{"deliveries:read", "deliveries:retry", "endpoints:write", "events:write"} {
		if _, err := tx.Exec(ctx, `INSERT INTO wde.api_key_scopes (workspace_id,api_key_id,scope) VALUES ($1,$2,$3)`, workspaceID, keyID, scope); err != nil {
			return BootstrapResult{}, err
		}
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT wde.append_audit_event($1,$2,'admin_cli','bootstrap','credential.bootstrap','workspace',$3,$4,'accepted',NULL)`,
		auditID, workspaceID, workspaceID.String(), "cli_"+auditID.String()); err != nil {
		return BootstrapResult{}, err
	}
	result := BootstrapResult{WorkspaceID: workspaceID, APIKeyID: keyID, Token: token}
	cleanup, err := sink(result)
	if err != nil {
		return BootstrapResult{}, err
	}
	committed := false
	defer func() {
		if !committed && cleanup != nil {
			cleanup()
		}
	}()
	if err := tx.Commit(ctx); err != nil {
		return BootstrapResult{}, err
	}
	committed = true
	return result, nil
}

func (s *AdminStore) Revoke(ctx context.Context, prefix string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var keyID, workspaceID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id,workspace_id FROM wde.api_keys WHERE prefix=$1 AND status='active' FOR UPDATE`, prefix).
		Scan(&keyID, &workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wde.api_keys SET status='revoked',revoked_at=clock_timestamp() WHERE id=$1`, keyID); err != nil {
		return false, err
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SELECT wde.append_audit_event($1,$2,'admin_cli','revoke','credential.revoke','api_key',$3,$4,'revoked',NULL)`,
		auditID, workspaceID, keyID.String(), "cli_"+auditID.String()); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
