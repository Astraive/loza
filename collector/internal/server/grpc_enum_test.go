package server

import (
	"testing"

	lozav1 "github.com/astraive/loza/gen/go/loza/core"
)

func TestEventProtoToMapEnumValues(t *testing.T) {
	tests := []struct {
		name    string
		level   lozav1.EventLevel
		outcome lozav1.EventOutcome
		want    map[string]string
	}{
		{
			name:    "canonical values",
			level:   lozav1.EventLevel_EVENT_LEVEL_INFO,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_SUCCESS,
			want: map[string]string{
				"level":   "info",
				"outcome": "success",
			},
		},
		{
			name:    "unspecified values omitted",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := eventProtoToMap(&lozav1.Event{Level: tt.level, Outcome: tt.outcome})
			if err != nil {
				t.Fatalf("eventProtoToMap() error = %v", err)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Errorf("eventProtoToMap()[%q] = %v, want %q", key, got[key], want)
				}
			}
			for _, key := range []string{"level", "outcome"} {
				if _, wantPresent := tt.want[key]; !wantPresent {
					if _, gotPresent := got[key]; gotPresent {
						t.Errorf("eventProtoToMap() unexpectedly contains %q: %v", key, got[key])
					}
				}
			}
		})
	}
}
