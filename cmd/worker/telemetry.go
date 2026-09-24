package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
)

type deliveryTelemetry struct {
	service *telemetry.Service
	active  atomic.Bool
}

func (o *deliveryTelemetry) WorkerActive(active bool) {
	o.active.Store(active)
	o.service.Metrics().SetWorkerActive(active)
}
func (o *deliveryTelemetry) Inflight(value int) { o.service.Metrics().SetInflight(value) }
func (o *deliveryTelemetry) Active() bool       { return o.active.Load() }

func (o *deliveryTelemetry) StartClaim(ctx context.Context, requested int) (context.Context, func(int, error)) {
	return o.service.StartClaim(ctx, requested)
}

func (o *deliveryTelemetry) StartAttempt(ctx context.Context, claim delivery.Claim) (
	context.Context, func(delivery.Result, bool, error),
) {
	ctx, finish := o.service.StartAttempt(ctx, telemetry.AttemptIDs{
		Event: claim.EventID.String(), Delivery: claim.DeliveryID.String(), Attempt: claim.AttemptID.String(),
		Number: int(claim.AttemptNumber),
	})
	return ctx, func(result delivery.Result, changed bool, err error) {
		deadLetter := result.Disposition == delivery.DispositionRetry && claim.AttemptNumber >= claim.MaxAttempts
		finish(telemetry.AttemptResult{Outcome: string(result.Disposition), Category: result.Category,
			Duration: time.Duration(result.DurationMS) * time.Millisecond,
			Retry:    result.Disposition == delivery.DispositionRetry && !deadLetter, DeadLetter: deadLetter,
			Changed: changed, Failed: err != nil})
	}
}

func (o *deliveryTelemetry) StartFinalization(ctx context.Context, claim delivery.Claim) (
	context.Context, func(bool, error),
) {
	return o.service.StartFinalization(ctx, telemetry.PersistenceIDs{
		Delivery: claim.DeliveryID.String(), Attempt: claim.AttemptID.String(),
	})
}
