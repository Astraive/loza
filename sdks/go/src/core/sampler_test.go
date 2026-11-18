package core

import (
	"math"
	"testing"
	"time"
)

func TestSampleRateLimited(t *testing.T) {
	s := SampleRateLimited(1, 40*time.Millisecond)
	if !s.ShouldSample(nil) {
		t.Fatalf("expected first event to be sampled")
	}
	if s.ShouldSample(nil) {
		t.Fatalf("expected second immediate event to be rate-limited")
	}
	time.Sleep(50 * time.Millisecond)
	if !s.ShouldSample(nil) {
		t.Fatalf("expected sampling budget to refill after window")
	}
}

func TestSampleSlowRequestsNumericThresholds(t *testing.T) {
	slow := &Event{DurationMS: 150}
	fast := &Event{DurationMS: 50}

	// All numeric threshold types keep a slow event and drop a fast one.
	thresholds := []any{
		100, // int (the previously unsupported float64 literal is covered below)
		int8(100), int16(100), int32(100), int64(100),
		uint(100), uint8(100), uint16(100), uint32(100), uint64(100),
		float32(100), float64(100.0),
		100 * time.Millisecond, // time.Duration
	}
	for _, threshold := range thresholds {
		s := SampleSlowRequests(threshold)
		if !s.ShouldSample(slow) {
			t.Errorf("SampleSlowRequests(%T(%v)) should keep a 150ms event", threshold, threshold)
		}
		if s.ShouldSample(fast) {
			t.Errorf("SampleSlowRequests(%T(%v)) should drop a 50ms event", threshold, threshold)
		}
	}

	// Non-numeric and non-finite thresholds stay invalid and drop every event.
	for _, threshold := range []any{"invalid", math.NaN(), math.Inf(1), math.Inf(-1)} {
		if SampleSlowRequests(threshold).ShouldSample(slow) {
			t.Errorf("SampleSlowRequests(%T(%v)) should drop every event", threshold, threshold)
		}
	}
}

func TestSampleByHeader(t *testing.T) {
	ev := &Event{
		Attrs: []Attr{
			String("http.header.x-debug-logging", "true"),
		},
	}

	if !SampleByHeader("X-Debug-Logging", "true").ShouldSample(ev) {
		t.Fatalf("expected matching header sampler to keep event")
	}
	if SampleByHeader("X-Debug-Logging", "false").ShouldSample(ev) {
		t.Fatalf("expected non-matching header sampler to drop event")
	}
}

