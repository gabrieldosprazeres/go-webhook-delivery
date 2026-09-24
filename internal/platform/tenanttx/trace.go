package tenanttx

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func startTransactionSpan(ctx context.Context) (context.Context, func(bool)) {
	provider := trace.SpanFromContext(ctx).TracerProvider()
	ctx, span := provider.Tracer("github.com/gabrieldosprazeres/go-webhook-delivery").Start(ctx,
		"wde.persistence.transaction", trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("db.operation.name", "transaction")))
	return ctx, func(ok bool) {
		result := "committed"
		if !ok {
			result = "rolled_back"
			span.SetStatus(codes.Error, "transaction failed")
		}
		span.SetAttributes(attribute.String("wde.persistence.result", result))
		span.End()
	}
}
