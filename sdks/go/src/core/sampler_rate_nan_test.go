package core

import (
	"math"
	"testing"
	"time"
)

// TestSampleRateLimitedRejectsInvalidRate verifies that non-finite rates do not
// create a token bucket whose NaN/+Inf tokens make every event pass through.
// A rate-limited sampler configured with an invalid rate must behave like
// SampleNone (drop everything) rather than silently keeping all events.
func TestSampleRateLimitedRejectsInvalidRate(t *testing.T) {
	const window = time.Second

	invalidRates := map[string]float64{
		"NaN":          math.NaN(),
		"positive inf": math.Inf(1),
		"negative inf": math.Inf(-1),
	}

	for name, rate := range invalidRates {
		t.Run(name, func(t *testing.T) {
			s := SampleRateLimited(rate, window)
			if s == nil {
				t.Fatal("expected a non-nil sampler")
			}
			for i := 0; i < 5; i++ {
				if s.ShouldSample(nil) {
					t.Fatalf("event %d was sampled with %s rate; want all events dropped", i, name)
				}
			}
		})
	}
}

// TestSampleRateLimitedValidRateStillWorks guards against over-restricting the
// fix: a normal finite rate must still produce a working token bucket.
func TestSampleRateLimitedValidRateStillWorks(t *testing.T) {
	s := SampleRateLimited(2, time.Minute)
	if !s.ShouldSample(nil) {
		t.Fatal("expected first event to be sampled")
	}
	if !s.ShouldSample(nil) {
		t.Fatal("expected second event to be sampled")
	}
	if s.ShouldSample(nil) {
		t.Fatal("expected third immediate event to be rate-limited")
	}
}
