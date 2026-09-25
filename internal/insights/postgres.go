package insights

import (
	"context"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Snapshot(ctx context.Context, workspaceID uuid.UUID) (Snapshot, error) {
	result := Snapshot{Window: "24h", Series: []Point{}}
	err := tenanttx.Within(ctx, s.pool, workspaceID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='2s'`); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `WITH d AS(
			SELECT count(*) deliveries,
			 count(*) FILTER(WHERE status='pending') pending,
			 count(*) FILTER(WHERE status='processing') processing,
			 count(*) FILTER(WHERE status='retry_scheduled') retry_scheduled,
			 count(*) FILTER(WHERE status='succeeded') succeeded,
			 count(*) FILTER(WHERE status='failed_permanent') failed_permanent,
			 count(*) FILTER(WHERE status='dead_letter') dead_letter,
				 COALESCE(GREATEST(EXTRACT(EPOCH FROM statement_timestamp()-(min(next_attempt_at)
				   FILTER(WHERE status IN('pending','retry_scheduled') AND next_attempt_at<=statement_timestamp()))),0),0) oldest
			FROM wde.deliveries WHERE workspace_id=$1 AND created_at>=statement_timestamp()-interval '24 hours'
		), a AS(
			SELECT count(*) attempts,count(*) FILTER(WHERE outcome='retry') retries,
			 COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY duration_ms)
			   FILTER(WHERE duration_ms IS NOT NULL),0) p95
			FROM wde.delivery_attempts WHERE workspace_id=$1
			  AND finished_at>=statement_timestamp()-interval '24 hours'
		), e AS(
			SELECT count(*) events FROM wde.events WHERE workspace_id=$1
			  AND created_at>=statement_timestamp()-interval '24 hours'
		)
		SELECT statement_timestamp(),e.events,d.deliveries,d.pending,d.processing,d.retry_scheduled,
		 d.succeeded,d.failed_permanent,d.dead_letter,a.attempts,a.retries,a.p95,d.oldest FROM d,a,e`, workspaceID).
			Scan(&result.Summary.GeneratedAt, &result.Summary.Events, &result.Summary.Deliveries,
				&result.Summary.Pending, &result.Summary.Processing, &result.Summary.RetryScheduled,
				&result.Summary.Succeeded, &result.Summary.FailedPermanent, &result.Summary.DeadLetter,
				&result.Summary.Attempts, &result.Summary.RetryAttempts, &result.Summary.P95DurationMS,
				&result.Summary.OldestReadySeconds); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH buckets AS(
			SELECT generate_series(date_trunc('hour',statement_timestamp())-interval '23 hours',
			 date_trunc('hour',statement_timestamp()),interval '1 hour') bucket
		), d AS(
			SELECT date_trunc('hour',created_at) bucket,count(*) deliveries,
			 count(*) FILTER(WHERE status='succeeded') succeeded,
			 count(*) FILTER(WHERE status IN('failed_permanent','dead_letter')) failed
			FROM wde.deliveries WHERE workspace_id=$1 AND created_at>=statement_timestamp()-interval '24 hours'
			GROUP BY 1
		), a AS(
			SELECT date_trunc('hour',finished_at) bucket,count(*) FILTER(WHERE outcome='retry') retries,
			 count(*) FILTER(WHERE outcome='permanent_failure') permanent
			FROM wde.delivery_attempts WHERE workspace_id=$1
			 AND finished_at>=statement_timestamp()-interval '24 hours' GROUP BY 1
		)
		SELECT b.bucket,COALESCE(d.deliveries,0),COALESCE(d.succeeded,0),COALESCE(d.failed,0),
		 COALESCE(a.retries,0),COALESCE(a.permanent,0) FROM buckets b
		LEFT JOIN d USING(bucket) LEFT JOIN a USING(bucket) ORDER BY b.bucket`, workspaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var point Point
			if err := rows.Scan(&point.Bucket, &point.Deliveries, &point.Succeeded, &point.Failed,
				&point.RetryAttempts, &point.PermanentErrors); err != nil {
				return err
			}
			result.Series = append(result.Series, point)
		}
		return rows.Err()
	})
	return result, err
}
