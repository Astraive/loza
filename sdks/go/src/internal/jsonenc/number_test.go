package jsonenc

import (
	"math"
	"strconv"
	"testing"
)

func TestAppendFloat64KeepsLargeFiniteValues(t *testing.T) {
	for _, value := range []float64{1.5e308, math.MaxFloat64, -1.5e308, -math.MaxFloat64} {
		got := string(AppendFloat64(nil, value))
		if got == "null" {
			t.Fatalf("finite float %v was encoded as null", value)
		}
		parsed, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q) for %v: %v", got, value, err)
		}
		if parsed != value {
			t.Fatalf("round trip = %v, want %v (encoded %q)", parsed, value, got)
		}
	}
}
