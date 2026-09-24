package runtime

import (
	"context"
	"sync"
	"time"
)

// ShutdownBudget provides one absolute deadline shared by every shutdown phase.
type ShutdownBudget struct {
	limit    time.Duration
	once     sync.Once
	deadline time.Time
}

func NewShutdownBudget(limit time.Duration) *ShutdownBudget {
	return &ShutdownBudget{limit: limit}
}

// Start fixes the absolute deadline at the first shutdown signal or fatal service error.
func (b *ShutdownBudget) Start() {
	b.once.Do(func() { b.deadline = time.Now().Add(b.limit) })
}

// Context starts the budget once and optionally applies a shorter phase limit.
func (b *ShutdownBudget) Context(maximum time.Duration) (context.Context, context.CancelFunc) {
	b.Start()
	deadline := b.deadline
	if maximum > 0 && time.Now().Add(maximum).Before(deadline) {
		deadline = time.Now().Add(maximum)
	}
	return context.WithDeadline(context.Background(), deadline)
}
