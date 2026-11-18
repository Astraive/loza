package bench

import (
	"context"
	"testing"

	"github.com/astraive/loza/sdks/go"
	"github.com/astraive/loza/sdks/go/src/core"
)

// An explicit, pre-existing trace context distinguishes the "WithTraceContext"
// emit benchmarks from BenchmarkEmitWithGeneratedTraceContext, which measures
// the code path that generates IDs. See issue #646.
const (
	benchExistingTraceID = "0af7651916cd43dd8448eb211c80319c"
	benchExistingSpanID  = "00f067aa0ba902b7"
)

// existingTraceContextParams returns Params carrying an explicit trace context
// so emit benchmarks measure the existing-context code path, not ID generation.
func existingTraceContextParams(event string) loza.Params {
	return loza.Params{
		Event:   event,
		TraceID: benchExistingTraceID,
		SpanID:  benchExistingSpanID,
	}
}

// startEventWithExistingTraceContext is the shared per-iteration operation used
// by the "WithTraceContext" benchmarks. Keeping it in one place lets the
// regression test assert that these benchmarks really supply a trace context.
func startEventWithExistingTraceContext() context.Context {
	return loza.StartEvent(context.Background(), existingTraceContextParams("bench.emit"))
}

// BenchmarkEmit measures basic emit latency (no auth, no sampler).
func BenchmarkEmit(b *testing.B) {
	sink, _ := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	_ = loza.Configure(cfg)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx := loza.StartEvent(context.Background(), loza.Params{Event: "bench.emit"})
		loza.Finish(ctx, "success")
		_ = loza.Emit(ctx)
	}
}

// --- Trace Context Benchmarks ---

func BenchmarkTraceContextGenerate(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc := core.GenerateTraceContext()
		_ = tc.TraceID
		_ = tc.SpanID
	}
}

func BenchmarkTraceContextGenerateTraceID(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = core.GenerateTraceID()
	}
}

func BenchmarkTraceContextGenerateSpanID(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = core.GenerateSpanID()
	}
}

func BenchmarkEmitWithExistingTraceContext(b *testing.B) {
	sink, _ := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	_ = loza.Configure(cfg)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx := startEventWithExistingTraceContext()
		loza.Finish(ctx, "success")
		_ = loza.Emit(ctx)
	}
}

func BenchmarkEmitWithGeneratedTraceContext(b *testing.B) {
	sink, _ := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	_ = loza.Configure(cfg)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx := loza.StartEvent(context.Background(), loza.Params{Event: "bench.emit"})
		loza.Finish(ctx, "success")
		_ = loza.Emit(ctx)
	}
}

func BenchmarkEmitSampledOutNoTraceGeneration(b *testing.B) {
	sink, _ := loza.MemorySink()
	cfg := loza.Test().WithSink(sink).WithSampler(loza.SampleNone())
	_ = loza.Configure(cfg)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx := loza.StartEvent(context.Background(), loza.Params{Event: "bench.emit"})
		loza.Finish(ctx, "success")
		_ = loza.Emit(ctx)
	}
}

func BenchmarkEmitBatch100WithTraceContext(b *testing.B) {
	sink, _ := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	_ = loza.Configure(cfg)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j := 0; j < 100; j++ {
			ctx := startEventWithExistingTraceContext()
			loza.Finish(ctx, "success")
			_ = loza.Emit(ctx)
		}
	}
}

// TestEmitBatch100WithTraceContextParams verifies that the "WithTraceContext"
// batch benchmark supplies an explicit, existing trace context rather than
// relying on generated IDs. Regression test for issue #646.
func TestEmitBatch100WithTraceContextParams(t *testing.T) {
	sink, _ := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	params := existingTraceContextParams("bench.emit")
	if params.TraceID == "" || params.SpanID == "" {
		t.Fatalf("existingTraceContextParams returned no trace context: %+v", params)
	}
	if params.TraceID != benchExistingTraceID || params.SpanID != benchExistingSpanID {
		t.Fatalf("existingTraceContextParams returned unexpected context: got trace=%q span=%q, want trace=%q span=%q",
			params.TraceID, params.SpanID, benchExistingTraceID, benchExistingSpanID)
	}

	// The exact operation used by BenchmarkEmitBatch100WithTraceContext must
	// carry the provided IDs into the active event.
	ctx := startEventWithExistingTraceContext()
	if got := loza.TraceIDFromContext(ctx); got != benchExistingTraceID {
		t.Errorf("TraceIDFromContext = %q, want %q", got, benchExistingTraceID)
	}
	if got := loza.SpanIDFromContext(ctx); got != benchExistingSpanID {
		t.Errorf("SpanIDFromContext = %q, want %q", got, benchExistingSpanID)
	}
}
