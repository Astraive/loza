package otel

import (
	"context"

	"github.com/astraive/loza/sdks/go"
	"go.opentelemetry.io/otel/trace"
)

// TraceAttrs extracts trace context identifiers from ctx as loza attrs.
func TraceAttrs(ctx context.Context) []loza.Attr {
	if ctx == nil {
		return nil
	}
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.IsValid() {
		return nil
	}
	return []loza.Attr{
		loza.TraceID(sc.TraceID().String()),
		loza.SpanID(sc.SpanID().String()),
	}
}

// EnrichTrace extracts and appends trace attrs to an active LOZA event in ctx.
// It returns the error from loza.Enrich so callers can detect failed enrichment
// (for example when the active event is no longer mutable). It returns nil when
// there are no trace attrs to add.
func EnrichTrace(ctx context.Context) error {
	attrs := TraceAttrs(ctx)
	if len(attrs) == 0 {
		return nil
	}
	return loza.Enrich(ctx, attrs...)
}
