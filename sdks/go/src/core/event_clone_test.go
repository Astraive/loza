package core

import "testing"

func TestEventCloneDeepCopy(t *testing.T) {
	ev := &Event{
		EventID: "evt-1",
		Attrs: []Attr{
			Group("user", String("id", "u-1")),
		},
		Checkpoints: []EventCheckpoint{
			{Name: "started", AtMS: 5, Attrs: []Attr{String("stage", "one")}},
		},
		Error: &ErrorInfo{Type: "x", Message: "boom"},
	}
	_ = ev.MarkEmitted()

	clone := ev.Clone()
	if clone == nil {
		t.Fatalf("expected clone")
	}
	if clone == ev {
		t.Fatalf("expected distinct clone pointer")
	}
	if clone.IsEmitted() {
		t.Fatalf("expected clone emitted flag to be reset")
	}
	if clone.Error == ev.Error {
		t.Fatalf("expected cloned error pointer")
	}

	clone.Error.Message = "changed"
	if ev.Error.Message != "boom" {
		t.Fatalf("expected original error to remain unchanged")
	}

	groupAttrs, ok := clone.Attrs[0].Value.([]Attr)
	if !ok {
		t.Fatalf("expected grouped attrs in clone")
	}
	groupAttrs[0].Value = "u-2"
	clone.Attrs[0].Value = groupAttrs

	origGroup, ok := ev.Attrs[0].Value.([]Attr)
	if !ok {
		t.Fatalf("expected grouped attrs in original")
	}
	if origGroup[0].Value.(string) != "u-1" {
		t.Fatalf("expected original attr to remain unchanged")
	}
}

// TestEventCloneResetsEmittedStateConsistently reproduces the reported defect
// where Clone copied raw state but not the atomic emitted flag, leaving an
// emitted event's clone with State()==Emitted and IsEmitted()==false. It also
// verifies that process numbering continues on the clone instead of restarting.
func TestEventCloneResetsEmittedStateConsistently(t *testing.T) {
	ev := &Event{EventID: "evt-clone-state"}

	// Advance the process-step counter through the public API.
	for i := 0; i < 3; i++ {
		if _, err := ev.StartProcess("step"); err != nil {
			t.Fatalf("StartProcess: %v", err)
		}
	}
	if !ev.MarkEmitted() {
		t.Fatalf("expected original event to become emitted")
	}

	clone := ev.Clone()
	if clone == nil {
		t.Fatalf("expected clone")
	}

	// State and the atomic emitted flag must agree, and the emitted state must
	// be reset so the clone remains usable.
	if clone.IsEmitted() != (clone.State() == EventStateEmitted) {
		t.Fatalf("clone state and emitted flag disagree: State()=%s IsEmitted()=%v", clone.State(), clone.IsEmitted())
	}
	if clone.State() == EventStateEmitted {
		t.Fatalf("expected clone emitted state to be reset, got %s", clone.State())
	}
	if ev.State() != EventStateEmitted || !ev.IsEmitted() {
		t.Fatalf("original event changed: State()=%s IsEmitted()=%v", ev.State(), ev.IsEmitted())
	}

	// processStep is copied, so numbering continues at 4 rather than restarting at 1.
	handle, err := clone.StartProcess("continued")
	if err != nil {
		t.Fatalf("expected clone to be mutable after reset: %v", err)
	}
	if handle.step != 4 {
		t.Fatalf("expected process numbering to continue at 4, got %d", handle.step)
	}
}
