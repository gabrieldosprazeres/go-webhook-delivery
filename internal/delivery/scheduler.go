package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrJobPanic = errors.New("delivery job panicked")

type claimCall struct {
	claims []Claim
	err    error
}

func (r *Runner) Run(ctx context.Context) error {
	if r.observer != nil {
		r.observer.WorkerActive(true)
		defer r.observer.WorkerActive(false)
		defer r.observer.Inflight(0)
	}
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	done := make(chan error, r.concurrency)
	active := 0
	emptyPolls := int16(0)
	failureBackoff := int16(0)
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return r.drain(done, active, cancelWork, nil)
		case err := <-done:
			active--
			r.observeInflight(active)
			if errors.Is(err, ErrJobPanic) {
				cancelWork()
				return r.drain(done, active, cancelWork, err)
			}
			if err != nil {
				failureBackoff = nextBackoffStep(failureBackoff)
				resetTimer(timer, r.poll.Delay(failureBackoff))
			} else {
				failureBackoff = 0
			}
		case <-timer.C:
			slots := r.concurrency - active
			if slots <= 0 {
				timer.Reset(r.poll.Delay(0))
				continue
			}
			claims, err := r.claimResponsive(ctx, min(slots, r.batchSize))
			if err != nil {
				if ctx.Err() != nil {
					return r.drain(done, active, cancelWork, nil)
				}
				if errors.Is(err, ErrJobPanic) {
					cancelWork()
					return r.drain(done, active, cancelWork, err)
				}
				failureBackoff = nextBackoffStep(failureBackoff)
				timer.Reset(r.poll.Delay(failureBackoff))
				continue
			}
			for _, claim := range claims {
				active++
				r.startJob(workCtx, claim, done)
			}
			r.observeInflight(active)
			if len(claims) == 0 {
				failureBackoff = 0
				emptyPolls = nextBackoffStep(emptyPolls)
				timer.Reset(r.poll.Delay(emptyPolls))
			} else if active >= r.concurrency {
				emptyPolls = 0
				timer.Reset(r.poll.Delay(0))
			} else {
				emptyPolls = 0
				timer.Reset(0)
			}
		}
	}
}

func nextBackoffStep(current int16) int16 {
	if current < 32767 {
		return current + 1
	}
	return current
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func (r *Runner) observeInflight(active int) {
	if r.observer != nil {
		r.observer.Inflight(active)
	}
}

func (r *Runner) claimResponsive(ctx context.Context, limit int) ([]Claim, error) {
	completed := make(chan claimCall, 1)
	go func() {
		result := claimCall{}
		defer func() {
			if recovered := recover(); recovered != nil {
				result.err = fmt.Errorf("claim: %w: %v", ErrJobPanic, recovered)
			}
			completed <- result
		}()
		result.claims, result.err = r.claim(ctx, limit)
	}()
	select {
	case result := <-completed:
		return result.claims, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runner) startJob(ctx context.Context, claim Claim, done chan<- error) {
	go func() {
		err := func() (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%w: %v", ErrJobPanic, recovered)
				}
			}()
			return r.processClaim(ctx, claim)
		}()
		select {
		case done <- err:
		default:
		}
	}()
}

func (r *Runner) drain(done <-chan error, active int, cancel context.CancelFunc, result error) error {
	if active == 0 {
		return result
	}
	forceAfter := r.shutdown - 5*time.Second
	if forceAfter <= 0 {
		forceAfter = r.shutdown / 2
	}
	force := time.NewTimer(forceAfter)
	defer force.Stop()
	deadline := time.NewTimer(r.shutdown)
	defer deadline.Stop()
	forceSignal := force.C
	for active > 0 {
		select {
		case err := <-done:
			active--
			r.observeInflight(active)
			if err != nil && !errors.Is(err, context.Canceled) {
				result = errors.Join(result, err)
			}
		case <-forceSignal:
			cancel()
			forceSignal = nil
		case <-deadline.C:
			cancel()
			return result
		}
	}
	return result
}
