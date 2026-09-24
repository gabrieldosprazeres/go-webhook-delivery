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

var errRedirectDisabled = errors.New("redirect disabled")

type Runner struct {
	store          Store
	client         *http.Client
	materials      cryptobox.Materials
	profile        config.Profile
	allowHTTP      bool
	workerID       uuid.UUID
	poll           PollPolicy
	claimTimeout   time.Duration
	requestTimeout time.Duration
	leaseTTL       time.Duration
	shutdown       time.Duration
	concurrency    int
	batchSize      int
	workspaceLimit int
	endpointLimit  int
	retry          RetryPolicy
	now            func() time.Time
}

type RunnerOptions struct {
	Poll, ClaimTimeout, RequestTimeout, LeaseTTL, Shutdown time.Duration
	Concurrency, BatchSize                                 int
	WorkspaceLimit, EndpointLimit                          int
	Retry                                                  RetryPolicy
	Client                                                 *http.Client
	PollJitter                                             func(time.Duration) time.Duration
}

func NewRunner(store Store, materials cryptobox.Materials, profile config.Profile, allowHTTP bool, workerID uuid.UUID) *Runner {
	return NewRunnerWithOptions(store, materials, profile, allowHTTP, workerID, RunnerOptions{})
}

func NewRunnerWithOptions(store Store, materials cryptobox.Materials, profile config.Profile, allowHTTP bool, workerID uuid.UUID, opts RunnerOptions) *Runner {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 32 << 10
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errRedirectDisabled
		},
	}
	if opts.Client != nil {
		client = opts.Client
	}
	defaults := RunnerOptions{
		Poll: 250 * time.Millisecond, ClaimTimeout: 200 * time.Millisecond,
		RequestTimeout: 10 * time.Second,
		LeaseTTL:       30 * time.Second, Shutdown: 30 * time.Second,
		Concurrency: 8, BatchSize: 8, WorkspaceLimit: 2, EndpointLimit: 2,
		Retry: DefaultRetryPolicy(),
	}
	mergeRunnerOptions(&defaults, opts)
	if defaults.Shutdown > 30*time.Second {
		defaults.Shutdown = 30 * time.Second
	}
	return &Runner{
		store: store, client: client, materials: materials, profile: profile,
		allowHTTP: allowHTTP, workerID: workerID,
		poll:         defaultPollPolicy(defaults.Poll, opts.PollJitter),
		claimTimeout: defaults.ClaimTimeout, requestTimeout: defaults.RequestTimeout,
		leaseTTL: defaults.LeaseTTL,
		shutdown: defaults.Shutdown, concurrency: defaults.Concurrency,
		batchSize: defaults.BatchSize, workspaceLimit: defaults.WorkspaceLimit,
		endpointLimit: defaults.EndpointLimit, retry: defaults.Retry, now: time.Now,
	}
}

func mergeRunnerOptions(target *RunnerOptions, override RunnerOptions) {
	if override.Poll > 0 {
		target.Poll = override.Poll
	}
	if override.RequestTimeout > 0 {
		target.RequestTimeout = override.RequestTimeout
	}
	if override.ClaimTimeout > 0 {
		target.ClaimTimeout = override.ClaimTimeout
	}
	if override.LeaseTTL > 0 {
		target.LeaseTTL = override.LeaseTTL
	}
	if override.Shutdown > 0 {
		target.Shutdown = override.Shutdown
	}
	if override.Concurrency > 0 {
		target.Concurrency = override.Concurrency
	}
	if override.BatchSize > 0 {
		target.BatchSize = override.BatchSize
	}
	if override.WorkspaceLimit > 0 {
		target.WorkspaceLimit = override.WorkspaceLimit
	}
	if override.EndpointLimit > 0 {
		target.EndpointLimit = override.EndpointLimit
	}
	if override.Retry.Base > 0 {
		target.Retry = override.Retry
	}
}

func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	claims, err := r.claim(ctx, 1)
	if err != nil || len(claims) == 0 {
		return false, err
	}
	err = r.processClaim(ctx, claims[0])
	return true, err
}

func (r *Runner) claim(ctx context.Context, limit int) ([]Claim, error) {
	claimCtx, cancel := context.WithTimeout(ctx, r.claimTimeout)
	defer cancel()
	return r.store.ClaimBatch(claimCtx, ClaimRequest{
		WorkerID: r.workerID, LeaseTTL: r.leaseTTL, Limit: limit,
		WorkspaceLimit: min(r.workspaceLimit, limit), EndpointLimit: min(r.endpointLimit, limit),
	})
}

func (r *Runner) processClaim(ctx context.Context, claim Claim) error {
	result := r.deliver(ctx, claim)
	if result.Disposition == DispositionRetry {
		result.RetryAfter = r.retry.Delay(claim.AttemptNumber, result.RetryAfter)
	}
	_, err := r.store.Finalize(ctx, claim, r.workerID, result)
	return err
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
