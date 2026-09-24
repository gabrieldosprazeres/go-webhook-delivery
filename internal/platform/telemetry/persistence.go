package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type PersistenceIDs struct {
	Delivery, Attempt string
}

func (s *Service) StartClaim(ctx context.Context, requested int) (context.Context, func(int, error)) {
	ctx, span := s.Tracer().Start(ctx, "wde.persistence.claim", trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.Int("wde.claim.requested", requested)))
	return ctx, func(claimed int, err error) {
		result := "empty"
		if claimed > 0 {
			result = "claimed"
		}
		if err != nil {
			result = "error"
			span.SetStatus(codes.Error, "claim failed")
		}
		span.SetAttributes(attribute.String("wde.persistence.result", result),
			attribute.Int("wde.claim.count", claimed))
		span.End()
	}
}

func (s *Service) StartFinalization(ctx context.Context, ids PersistenceIDs) (context.Context, func(bool, error)) {
	ctx, span := s.Tracer().Start(ctx, "wde.persistence.finalize", trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("wde.delivery.id", ids.Delivery),
			attribute.String("wde.attempt.id", ids.Attempt)))
	return ctx, func(changed bool, err error) {
		result := "stale"
		if changed {
			result = "changed"
		}
		if err != nil {
			result = "error"
			span.SetStatus(codes.Error, "finalization failed")
		}
		span.SetAttributes(attribute.String("wde.persistence.result", result))
		span.End()
	}
}
