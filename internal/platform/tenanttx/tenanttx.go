// Package tenanttx scopes API database work to one authenticated workspace.
package tenanttx

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const rollbackTimeout = 2 * time.Second

type Beginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Within sets the tenant GUC transaction-locally on the same connection used by fn.
// The transaction is always committed or rolled back before its connection returns
// to the pool, including while unwinding a panic.
func Within(ctx context.Context, pool Beginner, workspaceID uuid.UUID, fn func(pgx.Tx) error) (err error) {
	ctx, finishSpan := startTransactionSpan(ctx)
	defer func() {
		recovered := recover()
		finishSpan(err == nil && recovered == nil)
		if recovered != nil {
			panic(recovered)
		}
	}()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}
