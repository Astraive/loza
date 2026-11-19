package core

import (
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

func TestAllSamplerNilSemantics(t *testing.T) {
	ev := &Event{}

	// A nil-only (or empty) sampler set must drop, matching AnySampler(nil)
	// and NotSampler(nil), so a misconfigured pipeline fails closed.
	if AllSampler(nil).ShouldSample(ev) {
		t.Fatal("AllSampler(nil) should drop, not keep every event")
	}
	if AllSampler(nil, nil).ShouldSample(ev) {
		t.Fatal("AllSampler(nil, nil) should drop")
	}
	if AllSampler().ShouldSample(ev) {
		t.Fatal("AllSampler() with no samplers should drop")
	}

	// Nils are ignored when at least one non-nil sampler is present.
	if !AllSampler(SampleAll(), nil).ShouldSample(ev) {
		t.Fatal("AllSampler should ignore nil samplers when a non-nil sampler exists")
	}
	if !AllSampler(nil, SampleAll()).ShouldSample(ev) {
		t.Fatal("AllSampler should ignore leading nil samplers")
	}
	if AllSampler(nil, SampleNone()).ShouldSample(ev) {
		t.Fatal("AllSampler should still honor non-nil samplers around nils")
	}

	// The three combinators stay consistent on nil.
	if AnySampler(nil).ShouldSample(ev) || NotSampler(nil).ShouldSample(ev) || AllSampler(nil).ShouldSample(ev) {
		t.Fatal("all combinators should drop on a nil sampler")
	}
}
