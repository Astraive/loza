package core

import (
	"encoding/json"
	"testing"
)

// TestEncodeEventRepeatedCompositeArrays guards the jsonenc.Sep migration at
// the call sites that previously hand-injected commas. Each of these arrays
// contains several composite elements and must remain valid JSON.
func TestEncodeEventRepeatedCompositeArrays(t *testing.T) {
	ev := &Event{
		Checkpoints: []EventCheckpoint{
			{Name: "one", AtMS: 1},
			{Name: "two", AtMS: 2},
			{Name: "three", AtMS: 3},
		},
		Processes: []EventProcess{
			{Step: 1, Name: "p1"},
			{Step: 2, Name: "p2"},
		},
		Groups: []EventGroup{
			{Name: "g1"},
			{Name: "g2"},
		},
		Timers: []EventTimer{
			{Name: "t1", DurationMS: 1},
			{Name: "t2", DurationMS: 2},
		},
	}

	enc := JSONEncoder()
	out, err := enc.EncodeEvent(nil, ev)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("encoded event is not valid JSON: %v\n%s", err, out)
	}
	for key, want := range map[string]int{
		"checkpoints": 3,
		"processes":   2,
		"groups":      2,
		"timers":      2,
	} {
		arr, ok := doc[key].([]any)
		if !ok {
			t.Fatalf("%s is not an array: %T", key, doc[key])
		}
		if len(arr) != want {
			t.Fatalf("%s has %d elements, want %d", key, len(arr), want)
		}
	}
}
