package core

import (
	"context"
	"reflect"
	"testing"
)

// TestMockSinkPauseResume verifies that pausing is implemented solely via the
// paused bool: writes are rejected while paused and accepted again after resume.
func TestMockSinkPauseResume(t *testing.T) {
	sink := NewMockSink()
	ev := &Event{Service: "svc", Event: "test.event"}

	if err := sink.WriteEvent(context.Background(), []byte("before"), ev); err != nil {
		t.Fatalf("WriteEvent() before pause = %v, want nil", err)
	}
	if got := sink.Len(); got != 1 {
		t.Fatalf("Len() before pause = %d, want 1", got)
	}

	sink.Pause()
	if err := sink.WriteEvent(context.Background(), []byte("during"), ev); err == nil {
		t.Fatal("WriteEvent() while paused = nil, want error")
	}
	if got := sink.Len(); got != 1 {
		t.Fatalf("Len() while paused = %d, want 1 (no event recorded)", got)
	}

	sink.Resume()
	if err := sink.WriteEvent(context.Background(), []byte("after"), ev); err != nil {
		t.Fatalf("WriteEvent() after resume = %v, want nil", err)
	}
	if got := sink.Len(); got != 2 {
		t.Fatalf("Len() after resume = %d, want 2", got)
	}
}

// TestMockSinkHasNoPauseChanField guards the removal of the unused pauseChan
// field: pausing must not depend on an unread/unwritten channel.
func TestMockSinkHasNoPauseChanField(t *testing.T) {
	typ := reflect.TypeOf(MockSink{})
	if _, ok := typ.FieldByName("pauseChan"); ok {
		t.Fatal("MockSink still declares an unused pauseChan field")
	}
}
