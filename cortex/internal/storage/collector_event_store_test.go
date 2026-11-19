package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
)

// TestCollectorBackedEventStoreWritesAreNotSilentlyDiscarded guards against the
// regression where Save and SaveBatch logged a warning and returned nil, giving
// callers a false success while dropping their events. Writes against a
// collector-backed (SourceOfTruth) store must now report an explicit error.
func TestCollectorBackedEventStoreWritesAreNotSilentlyDiscarded(t *testing.T) {
	store := newCollectorBackedEventStore(config.CollectorConfig{})
	ctx := context.Background()

	t.Run("Save", func(t *testing.T) {
		event := &models.Event{ID: "evt-1", Service: "checkout"}

		err := store.Save(ctx, event, &LifecycleData{EventID: event.ID})
		if err == nil {
			t.Fatal("Save silently discarded the event by returning nil")
		}
		if !errors.Is(err, ErrCollectorReadOnly) {
			t.Fatalf("Save error = %v, want ErrCollectorReadOnly", err)
		}
		if !strings.Contains(err.Error(), event.ID) {
			t.Fatalf("Save error %q does not identify the dropped event", err)
		}
	})

	t.Run("SaveBatch", func(t *testing.T) {
		events := []*models.Event{
			{ID: "evt-1", Service: "checkout"},
			{ID: "evt-2", Service: "checkout"},
		}

		err := store.SaveBatch(ctx, events, []*LifecycleData{{}, {}})
		if err == nil {
			t.Fatal("SaveBatch silently discarded the events by returning nil")
		}
		if !errors.Is(err, ErrCollectorReadOnly) {
			t.Fatalf("SaveBatch error = %v, want ErrCollectorReadOnly", err)
		}
		if !strings.Contains(err.Error(), "2 events") {
			t.Fatalf("SaveBatch error %q does not report the dropped event count", err)
		}
	})
}
