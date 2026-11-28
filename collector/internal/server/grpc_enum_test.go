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
			name:    "debug level",
			level:   lozav1.EventLevel_EVENT_LEVEL_DEBUG,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "debug"},
		},
		{
			name:    "info level",
			level:   lozav1.EventLevel_EVENT_LEVEL_INFO,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "info"},
		},
		{
			name:    "warn level",
			level:   lozav1.EventLevel_EVENT_LEVEL_WARN,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "warn"},
		},
		{
			name:    "error level",
			level:   lozav1.EventLevel_EVENT_LEVEL_ERROR,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "error"},
		},
		{
			name:    "notice level",
			level:   lozav1.EventLevel_EVENT_LEVEL_NOTICE,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "notice"},
		},
		{
			name:    "fatal level",
			level:   lozav1.EventLevel_EVENT_LEVEL_FATAL,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{"level": "fatal"},
		},
		{
			name:    "success outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_SUCCESS,
			want:    map[string]string{"outcome": "success"},
		},
		{
			name:    "error outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_ERROR,
			want:    map[string]string{"outcome": "error"},
		},
		{
			name:    "partial outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_PARTIAL,
			want:    map[string]string{"outcome": "partial"},
		},
		{
			name:    "abandoned outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_ABANDONED,
			want:    map[string]string{"outcome": "abandoned"},
		},
		{
			name:    "retried outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_RETRIED,
			want:    map[string]string{"outcome": "retried"},
		},
		{
			name:    "cancelled outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_CANCELLED,
			want:    map[string]string{"outcome": "cancelled"},
		},
		{
			name:    "timeout outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_TIMEOUT,
			want:    map[string]string{"outcome": "timeout"},
		},
		{
			name:    "skipped outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_SKIPPED,
			want:    map[string]string{"outcome": "skipped"},
		},
		{
			name:    "rejected outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_REJECTED,
			want:    map[string]string{"outcome": "rejected"},
		},
		{
			name:    "quarantined outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_QUARANTINED,
			want:    map[string]string{"outcome": "quarantined"},
		},
		{
			name:    "unknown outcome",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNKNOWN,
			want:    map[string]string{"outcome": "unknown"},
		},
		{
			name:    "unspecified values omitted",
			level:   lozav1.EventLevel_EVENT_LEVEL_UNSPECIFIED,
			outcome: lozav1.EventOutcome_EVENT_OUTCOME_UNSPECIFIED,
			want:    map[string]string{},
		},
		{
			name:    "unrecognized values omitted",
			level:   lozav1.EventLevel(999),
			outcome: lozav1.EventOutcome(999),
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
