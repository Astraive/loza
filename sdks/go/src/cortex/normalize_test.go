package cortex

import (
	"encoding/json"
	"math"
	"testing"
)

// TestNormalizeIncidentContextClampsNaN reproduces the reported defect: a NaN
// confidence survives the < 0 / > 1 comparisons and makes encoding/json reject
// the struct with "json: unsupported value: NaN". Normalization must map NaN to
// 0 so the value is always serializable.
func TestNormalizeIncidentContextClampsNaN(t *testing.T) {
	ctx := &IncidentContext{
		IncidentID: "inc/1",
		Timestamp:  "2026-01-01T00:00:00Z",
		Confidence: math.NaN(),
	}

	NormalizeIncidentContext(ctx)

	if math.IsNaN(ctx.Confidence) {
		t.Fatalf("expected NaN confidence to be normalized, got NaN")
	}
	if ctx.Confidence != 0 {
		t.Fatalf("expected confidence=0 after normalizing NaN, got %v", ctx.Confidence)
	}

	// The normalized struct must be JSON-serializable without an unsupported
	// NaN value error.
	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("json.Marshal failed after normalizing NaN confidence: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to read marshaled JSON: %v", err)
	}
	if got, ok := decoded["confidence"].(float64); !ok || got != 0 {
		t.Fatalf("expected serialized confidence=0, got %v", decoded["confidence"])
	}
}

// TestNormalizeIncidentContextClampsConfidenceRange guards the existing clamp
// behavior and covers infinities, which share the NaN comparison edge case.
func TestNormalizeIncidentContextClampsConfidenceRange(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{name: "below zero", input: -0.5, want: 0},
		{name: "negative infinity", input: math.Inf(-1), want: 0},
		{name: "within range", input: 0.75, want: 0.75},
		{name: "above one", input: 1.5, want: 1},
		{name: "positive infinity", input: math.Inf(1), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &IncidentContext{
				IncidentID: "inc/1",
				Timestamp:  "2026-01-01T00:00:00Z",
				Confidence: tt.input,
			}

			NormalizeIncidentContext(ctx)

			if ctx.Confidence != tt.want {
				t.Fatalf("expected confidence=%v, got %v", tt.want, ctx.Confidence)
			}
		})
	}
}
