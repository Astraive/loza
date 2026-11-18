package eventconv

import (
	"testing"
	"time"
)

func TestFromRawMapPreservesFractionalSecondsFromNumericTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		timestamp float64
		wantSec   int64
		wantNsec  int
	}{
		{
			name:      "half second (reported case)",
			timestamp: 1700000000.5,
			wantSec:   1700000000,
			wantNsec:  500000000,
		},
		{
			name:      "millisecond precision",
			timestamp: 1700000000.125,
			wantSec:   1700000000,
			wantNsec:  125000000,
		},
		{
			name:      "whole second unchanged",
			timestamp: 1700000000,
			wantSec:   1700000000,
			wantNsec:  0,
		},
		{
			name:      "negative fractional second",
			timestamp: -1.5,
			wantSec:   -2,
			wantNsec:  500000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := FromRawMap(map[string]any{
				"id":        "evt-1",
				"service":   "test-service",
				"kind":      "collector_event",
				"timestamp": tt.timestamp,
			}, "jsonl")
			if err != nil {
				t.Fatalf("FromRawMap() unexpected error: %v", err)
			}

			want := time.Unix(tt.wantSec, int64(tt.wantNsec)).UTC()
			if !event.Timestamp.Equal(want) {
				t.Errorf("Timestamp = %s (nsec=%d), want %s (nsec=%d)",
					event.Timestamp.Format(time.RFC3339Nano), event.Timestamp.Nanosecond(),
					want.Format(time.RFC3339Nano), want.Nanosecond())
			}
			if got := event.Timestamp.Nanosecond(); got != tt.wantNsec {
				t.Errorf("Nanosecond() = %d, want %d", got, tt.wantNsec)
			}
		})
	}
}
