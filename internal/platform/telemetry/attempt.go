package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type AttemptIDs struct {
	Event, Delivery, Attempt string
	Number                   int
}

type AttemptResult struct {
	Outcome, Category string
	Duration          time.Duration
	Retry, DeadLetter bool
	Changed           bool
	Failed            bool
}

func (s *Service) StartAttempt(ctx context.Context, ids AttemptIDs) (context.Context, func(AttemptResult)) {
	ctx, span := s.Tracer().Start(ctx, "webhook.delivery.attempt", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("wde.event.id", ids.Event),
			attribute.String("wde.delivery.id", ids.Delivery), attribute.String("wde.attempt.id", ids.Attempt),
			attribute.Int("wde.attempt.number", ids.Number)))
	return ctx, func(result AttemptResult) {
		category := boundedCategory(result.Category)
		outcome := result.Outcome
		if outcome != "success" && outcome != "retry" && outcome != "permanent_failure" {
			outcome = "other"
		}
		span.SetAttributes(attribute.String("wde.attempt.outcome", outcome),
			attribute.String("wde.attempt.category", category), attribute.Bool("wde.finalization.changed", result.Changed))
		if result.Failed || !result.Changed {
			span.SetStatus(codes.Error, "attempt finalization failed")
		}
		span.End()
		if !result.Failed {
			s.metrics.ObserveAttempt(outcome, category, result.Duration, result.Retry, result.DeadLetter, result.Changed)
		}
	}
}
