package delivery

import (
	"context"
	"time"
)

type QueueStats struct {
	Ready  int64
	Oldest time.Duration
}

func (s *PostgresStore) QueueStats(ctx context.Context) (QueueStats, error) {
	var result QueueStats
	var oldestSeconds float64
	err := s.pool.QueryRow(ctx, `SELECT ready_count,oldest_seconds FROM wde.delivery_queue_metrics()`).
		Scan(&result.Ready, &oldestSeconds)
	if oldestSeconds > 0 {
		result.Oldest = time.Duration(oldestSeconds * float64(time.Second))
	}
	return result, err
}
