package cortex

import (
	"math"
	"testing"
)

func TestValidateIncidentContextConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		wantErr    bool
	}{
		{name: "zero", confidence: 0.0, wantErr: false},
		{name: "one", confidence: 1.0, wantErr: false},
		{name: "midpoint", confidence: 0.5, wantErr: false},
		{name: "negative", confidence: -0.1, wantErr: true},
		{name: "above one", confidence: 1.1, wantErr: true},
		{name: "positive infinity", confidence: math.Inf(1), wantErr: true},
		{name: "negative infinity", confidence: math.Inf(-1), wantErr: true},
		{name: "NaN", confidence: math.NaN(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &IncidentContext{
				IncidentID: "inc/1",
				Timestamp:  "2026-01-01T00:00:00Z",
				Confidence: tt.confidence,
			}
			err := ValidateIncidentContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateIncidentContext() error = %v, wantErr %v (confidence %v)", err, tt.wantErr, tt.confidence)
			}
		})
	}
}
