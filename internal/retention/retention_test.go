package retention

import (
	"context"
	"errors"
	"testing"
	"time"
)

type drainingStore struct {
	remaining    int64
	oldest       time.Time
	backlogCalls int
}

func (s *drainingStore) Purge(_ context.Context, batch int) (Counts, error) {
	removed := min(s.remaining, int64(batch))
	s.remaining -= removed
	return Counts{Buckets: removed}, nil
}

func (s *drainingStore) Backlog(context.Context) (Backlog, error) {
	s.backlogCalls++
	if s.remaining == 0 {
		return Backlog{}, nil
	}
	return Backlog{Total: s.remaining, OldestExpiredAt: &s.oldest}, nil
}

func TestRunOnceDrainsMoreThanTwentyFourBatchesAndPublishesAggregateState(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	store := &drainingStore{remaining: 2501, oldest: now.Add(-2 * time.Hour)}
	var observed Report
	runner := NewRunner(store, time.Hour).WithObserver(func(report Report) { observed = report })
	runner.now = func() time.Time { return now }
	report := runner.RunOnce(context.Background())
	if report.Counts.Buckets != 2501 || report.Batches != 27 || report.Backlog != 0 ||
		report.OldestAge != 0 || report.LastOK != now || store.backlogCalls != 1 {
		t.Fatalf("report=%+v", report)
	}
	if err := runner.Ready(); err != nil || runner.Snapshot().Counts.Buckets != 2501 || observed.Batches != 27 {
		t.Fatalf("ready=%v snapshot=%+v observed=%+v", err, runner.Snapshot(), observed)
	}
}

func TestRunOnceDrainsHundredThousandWithOneExactBacklogScan(t *testing.T) {
	store := &drainingStore{remaining: 100_001, oldest: time.Now().Add(-time.Hour)}
	runner := NewRunner(store, time.Hour)
	report := runner.RunOnce(context.Background())
	if report.Counts.Buckets != 100_001 || report.Batches != 1002 || report.Backlog != 0 ||
		report.OldestAge != 0 || store.backlogCalls != 1 || report.Degraded {
		t.Fatalf("report=%+v backlog_calls=%d", report, store.backlogCalls)
	}
}

type stalledStore struct{ oldest time.Time }

func (s stalledStore) Purge(context.Context, int) (Counts, error) { return Counts{}, nil }
func (s stalledStore) Backlog(context.Context) (Backlog, error) {
	return Backlog{Total: 1, OldestExpiredAt: &s.oldest}, nil
}

func TestRunOnceDegradesWhenBacklogCannotProgress(t *testing.T) {
	oldest := time.Now().Add(-time.Hour)
	runner := NewRunner(stalledStore{oldest: oldest}, time.Hour)
	runner.RunOnce(context.Background())
	if !errors.Is(runner.Ready(), ErrDegraded) || !runner.Snapshot().Degraded || runner.Snapshot().OldestAge < time.Hour {
		t.Fatalf("ready=%v report=%+v", runner.Ready(), runner.Snapshot())
	}
}

type inconsistentEmptyStore struct{ oldest time.Time }

func (s inconsistentEmptyStore) Purge(context.Context, int) (Counts, error) { return Counts{}, nil }
func (s inconsistentEmptyStore) Backlog(context.Context) (Backlog, error) {
	return Backlog{OldestExpiredAt: &s.oldest}, nil
}

func TestRunOnceClearsOldestAgeWheneverBacklogIsEmpty(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	runner := NewRunner(inconsistentEmptyStore{oldest: now.Add(-time.Hour)}, time.Hour)
	runner.now = func() time.Time { return now }
	report := runner.RunOnce(context.Background())
	if report.Backlog != 0 || report.OldestAge != 0 || report.Degraded {
		t.Fatalf("report=%+v", report)
	}
}
