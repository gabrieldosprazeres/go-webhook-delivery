package tenanttx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTransactionSpanUsesContextProviderAndSafeResult(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "root")
	_, finish := startTransactionSpan(rootCtx)
	finish(false)
	root.End()
	spans := recorder.Ended()
	if len(spans) != 2 || spans[0].Name() != "wde.persistence.transaction" ||
		spans[0].Parent().SpanID() != spans[1].SpanContext().SpanID() {
		t.Fatalf("unexpected transaction spans: %#v", spans)
	}
	text := spans[0].Name() + fmt.Sprint(spans[0].Attributes()) + spans[0].Status().Description
	if strings.Contains(text, "CANARY") || !strings.Contains(text, "rolled_back") {
		t.Fatalf("unsafe or incomplete transaction span: %s", text)
	}
}
