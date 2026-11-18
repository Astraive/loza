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

// TestGetInt64StringValues is a regression test for MASS-0311: a plain numeric
// string (the normal JSON representation of *_ms fields) must not be silently
// zeroed just because time.ParseDuration requires a unit suffix.
func TestGetInt64StringValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int64
	}{
		{name: "plain numeric string", value: "1500", want: 1500},
		{name: "plain numeric zero", value: "0", want: 0},
		{name: "negative plain numeric string", value: "-250", want: -250},
		{name: "plain numeric with surrounding whitespace", value: " 1500 ", want: 1500},
		{name: "go duration milliseconds", value: "1500ms", want: 1500},
		{name: "go duration seconds", value: "2s", want: 2000},
		{name: "invalid string falls back to zero", value: "not-a-number", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getInt64(map[string]any{"value": tt.value}, "value")
			if got != tt.want {
				t.Fatalf("getInt64(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestGetInt64NumericTypes(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
	}{
		{name: "float64", value: float64(1500), want: 1500},
		{name: "int64", value: int64(1500), want: 1500},
		{name: "int", value: 1500, want: 1500},
		{name: "missing key", value: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{"value": tt.value}
			if tt.value == nil {
				delete(m, "value")
			}
			if got := getInt64(m, "value"); got != tt.want {
				t.Fatalf("getInt64(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseInt64StringSurfacesError(t *testing.T) {
	if _, err := parseInt64String("not-a-number"); err == nil {
		t.Fatal("parseInt64String(\"not-a-number\") expected an error, got nil")
	}
}

func TestGetFloatStringValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  float64
		ok    bool
	}{
		{name: "plain numeric string", value: "1500", want: 1500, ok: true},
		{name: "plain decimal string", value: "1500.5", want: 1500.5, ok: true},
		{name: "go duration string", value: "1500ms", want: 1500, ok: true},
		{name: "invalid string", value: "not-a-number", want: 0, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := getFloat(map[string]any{"value": tt.value}, "value")
			if got != tt.want || ok != tt.ok {
				t.Fatalf("getFloat(%q) = (%v, %v), want (%v, %v)", tt.value, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestFromRawMapPlainNumericLifecycleStrings exercises the full conversion path
// that consumes getInt64/getFloat (lines 121, 138-141, 165-168, 191-192).
func TestFromRawMapPlainNumericLifecycleStrings(t *testing.T) {
	raw := map[string]any{
		"id":          "evt-1",
		"timestamp":   "2026-01-15T10:00:00Z",
		"service":     "checkout",
		"kind":        "http",
		"duration_ms": "1500",
		"http": map[string]any{
			"status_code": "200",
		},
		"checkpoints": []any{
			map[string]any{"name": "start", "at_ms": "1500"},
		},
		"processes": []any{
			map[string]any{
				"name":          "charge",
				"status_code":   "201",
				"started_at_ms": "1000",
				"ended_at_ms":   "2500",
				"duration_ms":   "1500",
			},
		},
		"groups": []any{
			map[string]any{
				"name":          "payment",
				"status_code":   "202",
				"started_at_ms": "1000",
				"ended_at_ms":   "3000",
				"duration_ms":   "2000",
			},
		},
		"timers": []any{
			map[string]any{"name": "db", "duration_ms": "750", "status_code": "203"},
		},
	}

	event, err := FromRawMap(raw, "")
	if err != nil {
		t.Fatalf("FromRawMap returned error: %v", err)
	}

	if event.DurationMs != 1500 {
		t.Errorf("event.DurationMs = %v, want 1500", event.DurationMs)
	}
	if event.HTTP == nil || event.HTTP.StatusCode != 200 {
		t.Errorf("event.HTTP.StatusCode = %v, want 200", event.HTTP)
	}
	if len(event.Checkpoints) != 1 || event.Checkpoints[0].AtMs != 1500 {
		t.Errorf("checkpoint AtMs = %v, want 1500", event.Checkpoints)
	}
	if len(event.Processes) != 1 {
		t.Fatalf("got %d processes, want 1", len(event.Processes))
	}
	p := event.Processes[0]
	if p.StatusCode != 201 || p.StartedAtMs != 1000 || p.EndedAtMs != 2500 || p.DurationMs != 1500 {
		t.Errorf("process = %+v, want status=201 start=1000 end=2500 duration=1500", p)
	}
	if len(event.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(event.Groups))
	}
	g := event.Groups[0]
	if g.StatusCode != 202 || g.StartedAtMs != 1000 || g.EndedAtMs != 3000 || g.DurationMs != 2000 {
		t.Errorf("group = %+v, want status=202 start=1000 end=3000 duration=2000", g)
	}
	if len(event.Timers) != 1 {
		t.Fatalf("got %d timers, want 1", len(event.Timers))
	}
	tm := event.Timers[0]
	if tm.DurationMs != 750 || tm.StatusCode != 203 {
		t.Errorf("timer = %+v, want duration=750 status=203", tm)
	}
}
