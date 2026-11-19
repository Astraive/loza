package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
)

// TestListLifecycleSummariesChildCounts is a regression test for the DuckDB
// lifecycle summary query. The checkpoint/process/group/timer/link counts must
// equal the number of elements in the corresponding JSON arrays, including the
// zero case for a NULL/missing array. The previous query wrapped
// json_array_length in "(SELECT COUNT(*) FROM ...)", which never yielded the
// array length.
func TestListLifecycleSummariesChildCounts(t *testing.T) {
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "lifecycle.duckdb"))
	if err != nil {
		t.Fatalf("open duckdb storage: %v", err)
	}
	defer stor.Close()

	ctx := context.Background()
	if err := stor.Init(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	events := stor.Events()

	// An event with a distinct, non-trivial number of each lifecycle primitive.
	withChildren := &models.Event{
		ID:          "event-with-children",
		EventID:     "event-with-children",
		Timestamp:   time.Now().UTC(),
		Service:     "checkout",
		Event:       "order.placed",
		Kind:        models.EventKind("lifecycle"),
		Outcome:     "success",
		DurationMs:  42,
		Checkpoints: make([]*models.EventCheckpoint, 3),
		Processes:   make([]*models.EventProcess, 2),
		Groups:      make([]*models.EventGroup, 4),
		Timers:      make([]*models.EventTimer, 1),
		Links:       make([]*models.EventLink, 5),
	}
	if err := events.Save(ctx, withChildren, nil); err != nil {
		t.Fatalf("save event with children: %v", err)
	}

	// An event with no lifecycle primitives at all (all columns NULL).
	withoutChildren := &models.Event{
		ID:        "event-without-children",
		EventID:   "event-without-children",
		Timestamp: time.Now().UTC().Add(-time.Minute),
		Service:   "checkout",
		Event:     "order.placed",
		Kind:      models.EventKind("lifecycle"),
		Outcome:   "success",
	}
	if err := events.Save(ctx, withoutChildren, nil); err != nil {
		t.Fatalf("save event without children: %v", err)
	}

	summaries, total, err := events.ListLifecycleSummaries(ctx, &LifecycleFilter{Service: "checkout"})
	if err != nil {
		t.Fatalf("list lifecycle summaries: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total 2, got %d", total)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	byID := make(map[string]*models.LifecycleSummary, len(summaries))
	for _, s := range summaries {
		byID[s.EventID] = s
	}

	got := byID[withChildren.ID]
	if got == nil {
		t.Fatalf("missing summary for %s", withChildren.ID)
	}
	assertCounts(t, "event with children", got, 3, 2, 4, 1, 5)

	got = byID[withoutChildren.ID]
	if got == nil {
		t.Fatalf("missing summary for %s", withoutChildren.ID)
	}
	assertCounts(t, "event without children", got, 0, 0, 0, 0, 0)
}

func assertCounts(t *testing.T, name string, s *models.LifecycleSummary, cp, pr, gr, ti, li int) {
	t.Helper()
	if s.CheckpointCount != cp {
		t.Errorf("%s: CheckpointCount = %d, want %d", name, s.CheckpointCount, cp)
	}
	if s.ProcessCount != pr {
		t.Errorf("%s: ProcessCount = %d, want %d", name, s.ProcessCount, pr)
	}
	if s.GroupCount != gr {
		t.Errorf("%s: GroupCount = %d, want %d", name, s.GroupCount, gr)
	}
	if s.TimerCount != ti {
		t.Errorf("%s: TimerCount = %d, want %d", name, s.TimerCount, ti)
	}
	if s.LinkCount != li {
		t.Errorf("%s: LinkCount = %d, want %d", name, s.LinkCount, li)
	}
}
