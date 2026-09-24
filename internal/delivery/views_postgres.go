package delivery

import (
	"context"
	"errors"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) Get(ctx context.Context, workspaceID, deliveryID uuid.UUID) (Details, error) {
	var details Details
	err := tenanttx.Within(ctx, s.pool, workspaceID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id,event_id,endpoint_id,status,created_at,updated_at FROM wde.deliveries WHERE workspace_id=$1 AND id=$2`, workspaceID, deliveryID).Scan(&details.ID, &details.EventID, &details.EndpointID, &details.Status, &details.CreatedAt, &details.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,attempt_sequence,state,outcome,http_status,error_category,started_at,finished_at FROM wde.delivery_attempts WHERE workspace_id=$1 AND delivery_id=$2 ORDER BY attempt_sequence`, workspaceID, deliveryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		details.Attempts = []Attempt{}
		for rows.Next() {
			var attempt Attempt
			if err := rows.Scan(&attempt.ID, &attempt.Sequence, &attempt.State, &attempt.Outcome,
				&attempt.HTTPStatus, &attempt.ErrorCategory, &attempt.StartedAt, &attempt.FinishedAt); err != nil {
				return err
			}
			details.Attempts = append(details.Attempts, attempt)
		}
		return rows.Err()
	})
	return details, err
}

func (s *PostgresStore) List(ctx context.Context, workspaceID uuid.UUID, limit int, rawCursor string) (ListResult, error) {
	if limit < 1 || limit > 100 {
		return ListResult{}, ErrInvalidList
	}
	cursor, err := decodeCursor(s.cursorPepper, workspaceID, rawCursor)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: []Summary{}}
	err = tenanttx.Within(ctx, s.pool, workspaceID, func(tx pgx.Tx) error {
		var cursorTime, cursorID any
		if !cursor.CreatedAt.IsZero() {
			cursorTime, cursorID = cursor.CreatedAt, cursor.ID
		}
		rows, err := tx.Query(ctx, `SELECT id,event_id,endpoint_id,status,created_at,updated_at
			FROM wde.deliveries WHERE workspace_id=$1
			AND ($2::timestamptz IS NULL OR (created_at,id) < ($2,$3::uuid))
			ORDER BY created_at DESC,id DESC LIMIT $4`, workspaceID, cursorTime, cursorID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Summary
			if err := rows.Scan(&item.ID, &item.EventID, &item.EndpointID, &item.Status,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return ListResult{}, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor, err = encodeCursor(s.cursorPepper, workspaceID,
			listCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return result, err
}
