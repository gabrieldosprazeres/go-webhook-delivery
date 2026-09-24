package delivery

import (
	"context"
	"errors"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) RequestReplay(ctx context.Context, command ReplayCommand) (ReplayResult, error) {
	result := ReplayResult{DeliveryID: command.DeliveryID}
	err := tenanttx.Within(ctx, s.pool, command.WorkspaceID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT command_id,new_run_number,duplicate
			FROM wde.request_replay($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			command.CommandID, command.AuditID, command.WorkspaceID, command.DeliveryID,
			command.ActorID, command.KeyHash, command.Fingerprint, command.FingerprintVersion,
			command.Reason, command.RequestID).Scan(&result.CommandID, &result.RunNumber, &result.Duplicate)
	})
	if err == nil {
		return result, nil
	}
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "P0001" {
		return ReplayResult{}, err
	}
	switch databaseError.Message {
	case "replay_invalid":
		return ReplayResult{}, ErrInvalidReplay
	case "replay_conflict":
		return ReplayResult{}, ErrReplayConflict
	case "replay_not_found":
		return ReplayResult{}, ErrNotFound
	case "replay_payload_purged":
		return ReplayResult{}, ErrPayloadPurged
	case "replay_invalid_transition":
		return ReplayResult{}, ErrInvalidTransition
	default:
		return ReplayResult{}, err
	}
}
