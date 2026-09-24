package main

import (
	"context"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
)

func runQueueMetrics(ctx context.Context, store *delivery.PostgresStore, metrics *telemetry.Metrics) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		stats, err := store.QueueStats(sampleCtx)
		cancel()
		if err != nil {
			metrics.QueueSampleFailed()
		} else {
			metrics.SetQueue(stats.Ready, stats.Oldest)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
