// Package delivery owns queue claims, HTTP delivery and tenant-scoped delivery views.
package delivery

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type Runner struct {
	store          Store
	client         *http.Client
	materials      cryptobox.Materials
	profile        config.Profile
	allowHTTP      bool
	workerID       uuid.UUID
	poll           time.Duration
	requestTimeout time.Duration
	leaseTTL       time.Duration
	now            func() time.Time
}

func NewRunner(store Store, materials cryptobox.Materials, profile config.Profile, allowHTTP bool, workerID uuid.UUID) *Runner {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 32 << 10
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirect disabled")
		},
	}
	return &Runner{
		store: store, client: client, materials: materials, profile: profile,
		allowHTTP: allowHTTP, workerID: workerID, poll: 250 * time.Millisecond,
		requestTimeout: 10 * time.Second, leaseTTL: 30 * time.Second, now: time.Now,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			worked, err := r.ProcessOne(ctx)
			if err != nil {
				return err
			}
			delay := r.poll
			if worked {
				delay = 0
			}
			timer.Reset(delay)
		}
	}
}

func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	attemptID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	claim, ok, err := r.store.Claim(ctx, r.workerID, attemptID, r.leaseTTL)
	if err != nil || !ok {
		return false, err
	}
	success, status, duration, category := r.deliver(ctx, claim)
	_, err = r.store.Finalize(ctx, claim, r.workerID, success, status, duration, category)
	return true, err
}

func elapsed(start, end time.Time) int {
	value := end.Sub(start).Milliseconds()
	if value < 0 {
		return 0
	}
	if value > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(value)
}
