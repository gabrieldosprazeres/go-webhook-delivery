// Package insights provides bounded, tenant-scoped product observability.
package insights

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Summary struct {
	GeneratedAt        time.Time `json:"generated_at"`
	Events             int64     `json:"events"`
	Deliveries         int64     `json:"deliveries"`
	Pending            int64     `json:"pending"`
	Processing         int64     `json:"processing"`
	RetryScheduled     int64     `json:"retry_scheduled"`
	Succeeded          int64     `json:"succeeded"`
	FailedPermanent    int64     `json:"failed_permanent"`
	DeadLetter         int64     `json:"dead_letter"`
	Attempts           int64     `json:"attempts"`
	RetryAttempts      int64     `json:"retry_attempts"`
	P95DurationMS      float64   `json:"p95_duration_ms"`
	OldestReadySeconds float64   `json:"oldest_ready_seconds"`
}

type Point struct {
	Bucket          time.Time `json:"bucket"`
	Deliveries      int64     `json:"deliveries"`
	Succeeded       int64     `json:"succeeded"`
	Failed          int64     `json:"failed"`
	RetryAttempts   int64     `json:"retry_attempts"`
	PermanentErrors int64     `json:"permanent_errors"`
}

type Snapshot struct {
	Window  string  `json:"window"`
	Summary Summary `json:"summary"`
	Series  []Point `json:"series"`
}

type Store interface {
	Snapshot(context.Context, uuid.UUID) (Snapshot, error)
}
