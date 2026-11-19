package otel

import (
	"context"
	"testing"

	loza "github.com/astraive/loza/sdks/go"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// configureMemoryLogger installs a fresh in-memory logger for a test.
func configureMemoryLogger(t *testing.T) {
	t.Helper()
	sink, _ := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
}

func baggageContext(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	member, err := baggage.NewMember("tenant", "acme")
	if err != nil {
		t.Fatalf("baggage member: %v", err)
	}
	bg, err := baggage.New(member)
	if err != nil {
		t.Fatalf("baggage: %v", err)
	}
	return baggage.ContextWithBaggage(ctx, bg)
}

// TestEnrichBaggageReturnsEnrichError reproduces the reported defect: the
// error returned by loza.Enrich used to be discarded. Enriching an emitted
// (immutable) event must surface the failure to the caller.
func TestEnrichBaggageReturnsEnrichError(t *testing.T) {
	configureMemoryLogger(t)
	ctx := baggageContext(t, loza.StartEvent(context.Background(), loza.Params{Event: "otel.baggage"}))
	if err := loza.Finish(ctx, "success"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if err := EnrichBaggage(ctx, "tenant"); err == nil {
		t.Fatal("EnrichBaggage discarded the loza.Enrich error on an emitted event")
	}
}

// TestEnrichBaggageReturnsNilOnActiveEvent verifies the happy path still
// reports success when attrs are appended to a mutable event.
func TestEnrichBaggageReturnsNilOnActiveEvent(t *testing.T) {
	configureMemoryLogger(t)
	ctx := baggageContext(t, loza.StartEvent(context.Background(), loza.Params{Event: "otel.baggage"}))

	if err := EnrichBaggage(ctx, "tenant"); err != nil {
		t.Fatalf("EnrichBaggage on active event: %v", err)
	}
	if got, ok := loza.Get(ctx, "baggage.tenant"); !ok || got != "acme" {
		t.Fatalf("baggage attr = %v, %v", got, ok)
	}
}

// TestEnrichTraceReturnsEnrichError covers the adjacent EnrichTrace helper,
// which swallowed the same loza.Enrich error.
func TestEnrichTraceReturnsEnrichError(t *testing.T) {
	configureMemoryLogger(t)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		SpanID:  trace.SpanID{2, 2, 2, 2, 2, 2, 2, 2},
	})
	ctx := trace.ContextWithSpanContext(loza.StartEvent(context.Background(), loza.Params{Event: "otel.trace"}), sc)
	if err := loza.Finish(ctx, "success"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := loza.Emit(ctx); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if err := EnrichTrace(ctx); err == nil {
		t.Fatal("EnrichTrace discarded the loza.Enrich error on an emitted event")
	}
}

// TestEnrichHelpersReturnNilForNoAttrs documents that no-op paths return nil.
func TestEnrichHelpersReturnNilForNoAttrs(t *testing.T) {
	if err := EnrichBaggage(nil, "tenant"); err != nil {
		t.Fatalf("EnrichBaggage(nil): %v", err)
	}
	if err := EnrichBaggage(context.Background(), " "); err != nil {
		t.Fatalf("EnrichBaggage(empty allowlist): %v", err)
	}
	if err := EnrichTrace(nil); err != nil {
		t.Fatalf("EnrichTrace(nil): %v", err)
	}
}
