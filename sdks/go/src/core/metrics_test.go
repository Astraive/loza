package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsCollectorExportsPrometheusText(t *testing.T) {
	mc := NewMetricsCollector("loza_test", 8)
	mc.OnEventCreated()
	mc.OnRetry(12)
	mc.OnBackpressure()

	rec := httptest.NewRecorder()
	mc.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `loza_test_events_created_total 1`) {
		t.Fatalf("expected events_created_total in metrics output, got %s", body)
	}
	if !strings.Contains(body, `loza_test_retry_total{attempt="12"} 1`) {
		t.Fatalf("expected retry_total label in metrics output, got %s", body)
	}
	if !strings.Contains(body, `loza_test_backpressure_total 1`) {
		t.Fatalf("expected backpressure_total in metrics output, got %s", body)
	}
}

func TestPrometheusStatsHandlerExposesMetrics(t *testing.T) {
	h := NewPrometheusStatsHandler("loza_obs", 4)
	h.OnEmit(nil)
	h.OnDeliveryFailed(nil, nil)

	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `loza_obs_events_emitted_total{status="success"} 1`) {
		t.Fatalf("expected emitted success metric, got %s", body)
	}
	if !strings.Contains(body, `loza_obs_events_emitted_total{status="failure"} 1`) {
		t.Fatalf("expected emitted failure metric, got %s", body)
	}
}

func TestDecrementBufferSizeClampsAtZero(t *testing.T) {
	mc := NewMetricsCollector("loza_clamp", 8)
	for i := 0; i < 5; i++ {
		mc.OnEventDropped("clamp")
		if got := mc.currentBufferSize.Load(); got != 0 {
			t.Fatalf("decrement %d: currentBufferSize=%d, want 0", i, got)
		}
		if gauge := testutil.ToFloat64(mc.bufferSize); gauge != 0 {
			t.Fatalf("decrement %d: gauge=%v, want 0", i, gauge)
		}
	}
}

func TestDecrementBufferSizeDoesNotLoseConcurrentIncrements(t *testing.T) {
	const (
		rounds     = 500
		increments = 8
		decrements = 4
	)

	// With more increments than decrements, a correct clamp-at-zero counter
	// can never finish below increments-decrements, regardless of interleaving:
	// every decrement removes at most one from the count (clamping removes
	// less), while every increment is counted. The old Add(-1)/Store(0) pair
	// could overwrite an increment landing in its window and drop below this
	// floor, losing the update from both the counter and the gauge.
	const minSize = int64(increments - decrements)

	for round := 0; round < rounds; round++ {
		mc := NewMetricsCollector("loza_concurrent", 16)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(increments + decrements)

		for i := 0; i < decrements; i++ {
			go func() {
				defer wg.Done()
				<-start
				mc.OnEventDropped("race")
			}()
		}
		for i := 0; i < increments; i++ {
			go func() {
				defer wg.Done()
				<-start
				mc.OnEventCreated()
			}()
		}

		close(start)
		wg.Wait()

		if got := mc.currentBufferSize.Load(); got < minSize {
			t.Fatalf("round %d: lost concurrent increment: currentBufferSize=%d, want >= %d", round, got, minSize)
		}
	}
}

func TestLoggerFeedsPrometheusStatsHandler(t *testing.T) {
	sink, _ := MemorySink()
	stats := NewPrometheusStatsHandler("loza_hook", 4)
	l, err := New(Test().WithSink(sink).WithStatsHandler(stats))
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "metrics.test"})
	if err := l.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := l.Emit(ctx); err != nil {
		t.Fatalf("emit: %v", err)
	}

	rec := httptest.NewRecorder()
	stats.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `loza_hook_events_created_total 1`) {
		t.Fatalf("expected created counter in metrics output, got %s", body)
	}
	if !strings.Contains(body, `loza_hook_events_finished_total 1`) {
		t.Fatalf("expected finished counter in metrics output, got %s", body)
	}
	if !strings.Contains(body, `loza_hook_emit_duration_seconds_count 1`) {
		t.Fatalf("expected emit duration histogram in metrics output, got %s", body)
	}
}
