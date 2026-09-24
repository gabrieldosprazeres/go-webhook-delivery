// Package retention executes bounded, idempotent data lifecycle maintenance.
package retention

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrDegraded = errors.New("retention: maintenance degraded")

type Counts struct {
	Payloads, Secrets, Attempts, Replays, Rotations int64
	Deliveries, Events, Audits, Buckets             int64
	WorkspaceRows, WorkspaceSteps                   int64
}

func (c Counts) Progress() int64 {
	return c.Payloads + c.Secrets + c.Attempts + c.Replays + c.Rotations +
		c.Deliveries + c.Events + c.Audits + c.Buckets + c.WorkspaceRows + c.WorkspaceSteps
}

func (c *Counts) Add(other Counts) {
	c.Payloads += other.Payloads
	c.Secrets += other.Secrets
	c.Attempts += other.Attempts
	c.Replays += other.Replays
	c.Rotations += other.Rotations
	c.Deliveries += other.Deliveries
	c.Events += other.Events
	c.Audits += other.Audits
	c.Buckets += other.Buckets
	c.WorkspaceRows += other.WorkspaceRows
	c.WorkspaceSteps += other.WorkspaceSteps
}

type Backlog struct {
	Total           int64
	OldestExpiredAt *time.Time
}

type Report struct {
	Counts    Counts
	Backlog   int64
	OldestAge time.Duration
	Duration  time.Duration
	Batches   int
	LastOK    time.Time
	Degraded  bool
}

type Store interface {
	Purge(context.Context, int) (Counts, error)
	Backlog(context.Context) (Backlog, error)
}

type Runner struct {
	store    Store
	interval time.Duration
	budget   time.Duration
	now      func() time.Time
	mu       sync.RWMutex
	report   Report
	lastErr  error
	observe  func(Report)
}

func NewRunner(store Store, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Runner{store: store, interval: interval, budget: 15 * time.Minute, now: time.Now}
}

// WithObserver publishes aggregate maintenance state without tenant data.
func (r *Runner) WithObserver(observer func(Report)) *Runner {
	r.observe = observer
	return r
}

func (r *Runner) Run(ctx context.Context) error {
	r.RunOnce(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

func (r *Runner) RunOnce(ctx context.Context) Report {
	started := r.now()
	jobCtx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()
	report := Report{}
	var runErr error
	for {
		batch, err := r.store.Purge(jobCtx, 100)
		report.Batches++
		report.Counts.Add(batch)
		if err != nil {
			runErr = err
			break
		}
		if batch.Progress() > 0 {
			continue
		}
		backlog, err := r.store.Backlog(jobCtx)
		if err != nil {
			runErr = err
			break
		}
		report.Backlog = backlog.Total
		report.OldestAge = 0
		if backlog.Total > 0 && backlog.OldestExpiredAt != nil {
			report.OldestAge = max(r.now().Sub(*backlog.OldestExpiredAt), 0)
		}
		if backlog.Total == 0 {
			report.LastOK = r.now()
			break
		}
		runErr = ErrDegraded
		break
	}
	report.Duration = r.now().Sub(started)
	report.Degraded = runErr != nil
	r.mu.Lock()
	r.report, r.lastErr = report, runErr
	r.mu.Unlock()
	if r.observe != nil {
		r.observe(report)
	}
	return report
}

func (r *Runner) Snapshot() Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.report
}

func (r *Runner) Ready() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.lastErr != nil || r.report.LastOK.IsZero() || r.now().Sub(r.report.LastOK) > 25*time.Hour {
		return ErrDegraded
	}
	return nil
}
