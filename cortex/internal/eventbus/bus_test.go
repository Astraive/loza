package eventbus

import (
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

// TestWithBufferSizeControlsSubscriberCapacity verifies that WithBufferSize is
// not dead code: it must determine the capacity of newly created subscriber
// channels. Publish drops events once a subscriber is full, so the number of
// buffered events after publishing more than the capacity equals that capacity.
func TestWithBufferSizeControlsSubscriberCapacity(t *testing.T) {
	bus := New().WithBufferSize(3)
	ch := bus.Subscribe("")
	defer bus.Unsubscribe("", ch)

	for i := 0; i < 10; i++ {
		bus.Publish(&models.Event{})
	}

	if got := len(ch); got != 3 {
		t.Fatalf("configured buffer size: expected 3 buffered events, got %d", got)
	}
}

// TestNewUsesDefaultBufferSize verifies the unchanged default when no
// configured size is supplied.
func TestNewUsesDefaultBufferSize(t *testing.T) {
	bus := New()
	ch := bus.Subscribe("")
	defer bus.Unsubscribe("", ch)

	for i := 0; i < 200; i++ {
		bus.Publish(&models.Event{})
	}

	if got := len(ch); got != 128 {
		t.Fatalf("default buffer size: expected 128 buffered events, got %d", got)
	}
}
