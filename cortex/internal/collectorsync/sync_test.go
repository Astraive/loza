package collectorsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/collectorbridge"
	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
)

type fakeBatchProcessor struct {
	mu        sync.Mutex
	batches   [][]string
	err       error
	onProcess func()
}

func (f *fakeBatchProcessor) ProcessBatch(_ context.Context, events []*models.Event) error {
	if f.err != nil {
		return f.err
	}
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	f.mu.Lock()
	f.batches = append(f.batches, ids)
	f.mu.Unlock()
	if f.onProcess != nil {
		f.onProcess()
	}
	return nil
}

func (f *fakeBatchProcessor) FlattenedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, batch := range f.batches {
		ids = append(ids, batch...)
	}
	return ids
}

func TestRunSourceOfTruthSyncProcessesPollThenTail(t *testing.T) {
	t.Helper()
	pollEvent := map[string]any{
		"id":             "evt-1",
		"event_id":       "evt-1",
		"timestamp":      "2026-01-01T00:00:00Z",
		"kind":           "log",
		"event":          "log",
		"schema_version": "v1",
		"event_version":  "v1",
		"version":        "1",
		"service":        "checkout",
		"provenance":     "collector",
	}
	tailEvent := map[string]any{
		"id":             "evt-2",
		"event_id":       "evt-2",
		"event":          "log",
		"schema_version": "v1",
		"event_version":  "v1",
		"version":        "1",
		"timestamp":      "2026-01-01T00:00:01Z",
		"kind":           "log",
		"service":        "checkout",
		"provenance":     "collector",
	}

	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collectors/test/lql/query":
			call := queryCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if call == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"columns": []string{"raw"},
					"rows":    []map[string]any{{"raw": pollEvent}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"columns": []string{"raw"}, "rows": []map[string]any{}})
		case "/collectors/test/tail":
			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			_ = json.NewEncoder(w).Encode(tailEvent)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "collector.cursor")
	cfg := config.CollectorConfig{
		Collector:            "test",
		URL:                  server.URL,
		SourceOfTruth:        true,
		BatchSize:            10,
		TailEnabled:          true,
		TailBufferSize:       8,
		TailBatchSize:        1,
		TailFlushInterval:    10 * time.Millisecond,
		TailReconnectBackoff: 10 * time.Millisecond,
		QueryTable:           "events",
		RawColumn:            "raw",
		TimestampColumn:      "timestamp",
		CursorPath:           cursorPath,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processor := &fakeBatchProcessor{}
	processor.onProcess = func() {
		if len(processor.FlattenedIDs()) >= 2 {
			cancel()
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSourceOfTruthSync(ctx, cfg, processor)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for source-of-truth sync")
	}

	ids := processor.FlattenedIDs()
	if len(ids) < 2 || ids[0] != "evt-1" || ids[1] != "evt-2" {
		t.Fatalf("expected poll and tail events in order, got %v", ids)
	}

	client := collectorbridge.NewClient(cfg)
	cursor, err := client.LoadCursor()
	if err != nil {
		t.Fatalf("load cursor: %v", err)
	}
	if cursor.EventID != "evt-2" {
		t.Fatalf("expected cursor to advance to tail event, got %+v", cursor)
	}
}

func TestRunPollCatchupDoesNotAdvanceCursorOnFailure(t *testing.T) {
	t.Helper()
	event := map[string]any{
		"schema_version": "v1",
		"event_version":  "v1",
		"version":        "1",
		"event":          "log",
		"id":             "evt-1",
		"event_id":       "evt-1",
		"timestamp":      "2026-01-01T00:00:00Z",
		"kind":           "log",
		"service":        "checkout",
		"provenance":     "collector",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collectors/test/lql/query" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"columns": []string{"raw"},
			"rows":    []map[string]any{{"raw": event}},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	cursorPath := filepath.Join(dir, "collector.cursor")
	cfg := config.CollectorConfig{
		URL:             server.URL,
		Collector:       "test",
		SourceOfTruth:   true,
		BatchSize:       10,
		QueryTable:      "events",
		RawColumn:       "raw",
		TimestampColumn: "timestamp",
		CursorPath:      cursorPath,
	}

	client := collectorbridge.NewClient(cfg)
	state := &cursorState{}
	proc := &fakeBatchProcessor{err: errors.New("boom")}

	err := runPollCatchup(context.Background(), cfg, client, proc, state)
	if err == nil {
		t.Fatal("expected poll catch-up to fail")
	}
	if _, statErr := os.Stat(cursorPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no persisted cursor on failure, got stat err=%v", statErr)
	}
	if !state.Current().Timestamp.IsZero() || state.Current().EventID != "" {
		t.Fatalf("expected in-memory cursor to remain zero, got %+v", state.Current())
	}
}

func TestRunPollCatchupOrdersEqualTimestampsByEventID(t *testing.T) {
	const timestamp = "2026-01-01T00:00:00Z"
	makeEvent := func(id string) map[string]any {
		return map[string]any{
			"schema_version": "v1",
			"event_version":  "v1",
			"version":        "1",
			"event":          "log",
			"id":             id,
			"event_id":       id,
			"timestamp":      timestamp,
			"kind":           "log",
			"service":        "checkout",
			"provenance":     "collector",
		}
	}
	// Timestamp-only ordering may return this physical order, which can split
	// the equal-timestamp group before its smallest event ID is fetched.
	events := []map[string]any{makeEvent("evt-b"), makeEvent("evt-c"), makeEvent("evt-a")}
	var queryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collectors/test/lql/query" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Query      string `json:"query"`
			Parameters map[string]struct {
				Value string `json:"value"`
			} `json:"parameters"`
			Limit int `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode query request: %v", err)
			http.Error(w, "invalid query request", http.StatusBadRequest)
			return
		}
		queryCalls++
		compositeOrder := strings.Contains(request.Query, "| sort timestamp asc, event_id asc | limit 2")
		if !compositeOrder {
			t.Errorf("query lacks stable composite order: %q", request.Query)
		}
		if request.Limit != 2 {
			t.Errorf("query limit = %d, want 2", request.Limit)
		}
		after := ""
		if binding, ok := request.Parameters["cursor_id"]; ok {
			after = binding.Value
		}
		page := make([]map[string]any, 0, len(events))
		for _, event := range events {
			if event["event_id"].(string) > after {
				page = append(page, event)
			}
		}
		if compositeOrder {
			sort.Slice(page, func(i, j int) bool {
				return page[i]["event_id"].(string) < page[j]["event_id"].(string)
			})
		}
		if len(page) > request.Limit {
			page = page[:request.Limit]
		}
		rows := make([]map[string]any, 0, len(page))
		for _, event := range page {
			rows = append(rows, map[string]any{"raw": event})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"columns": []string{"raw"},
			"rows":    rows,
		}); err != nil {
			t.Errorf("encode query response: %v", err)
		}
	}))
	defer server.Close()

	cfg := config.CollectorConfig{
		URL:             server.URL,
		Collector:       "test",
		BatchSize:       2,
		QueryTable:      "events",
		RawColumn:       "raw",
		TimestampColumn: "timestamp",
		CursorPath:      filepath.Join(t.TempDir(), "collector.cursor"),
	}
	processor := &fakeBatchProcessor{}
	state := &cursorState{}
	if err := runPollCatchup(context.Background(), cfg, collectorbridge.NewClient(cfg), processor, state); err != nil {
		t.Fatalf("run poll catch-up: %v", err)
	}
	got := processor.FlattenedIDs()
	if len(got) != 3 || got[0] != "evt-a" || got[1] != "evt-b" || got[2] != "evt-c" {
		t.Fatalf("catch-up lost or reordered equal-timestamp events: %v", got)
	}
	if queryCalls != 2 {
		t.Fatalf("query calls = %d, want two pages", queryCalls)
	}
}

func TestCursorLessZeroTimestamps(t *testing.T) {
	zero := time.Time{}
	later := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		a    collectorbridge.Cursor
		b    collectorbridge.Cursor
		want bool
	}{
		{
			name: "zero timestamps ascending ids",
			a:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			b:    collectorbridge.Cursor{Timestamp: zero, EventID: "b"},
			want: true,
		},
		{
			name: "zero timestamps descending ids",
			a:    collectorbridge.Cursor{Timestamp: zero, EventID: "b"},
			b:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			want: false,
		},
		{
			name: "zero timestamps equal ids are not less",
			a:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			b:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			want: false,
		},
		{
			name: "empty cursor precedes zero timestamp id",
			a:    collectorbridge.Cursor{Timestamp: zero},
			b:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			want: true,
		},
		{
			name: "zero timestamp id does not precede empty cursor",
			a:    collectorbridge.Cursor{Timestamp: zero, EventID: "a"},
			b:    collectorbridge.Cursor{Timestamp: zero},
			want: false,
		},
		{
			name: "zero timestamp precedes real timestamp",
			a:    collectorbridge.Cursor{Timestamp: zero, EventID: "z"},
			b:    collectorbridge.Cursor{Timestamp: later, EventID: "a"},
			want: true,
		},
		{
			name: "real timestamp does not precede zero timestamp",
			a:    collectorbridge.Cursor{Timestamp: later, EventID: "a"},
			b:    collectorbridge.Cursor{Timestamp: zero, EventID: "z"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cursorLess(tc.a, tc.b); got != tc.want {
				t.Fatalf("cursorLess(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCursorForEventsZeroTimestampsPicksGreatestID(t *testing.T) {
	events := []*models.Event{
		{ID: "c"},
		{ID: "a"},
		{ID: "b"},
	}

	got := cursorForEvents(events)
	if !got.Timestamp.IsZero() || got.EventID != "c" {
		t.Fatalf("expected zero-timestamp cursor with greatest ID c, got %+v", got)
	}
}

func TestIsEventAfterCursorZeroTimestamps(t *testing.T) {
	cursor := collectorbridge.Cursor{EventID: "b"}

	if isEventAfterCursor(&models.Event{ID: "a"}, cursor) {
		t.Fatal("event with smaller ID must not be after a zero-timestamp cursor with larger ID")
	}
	if !isEventAfterCursor(&models.Event{ID: "c"}, cursor) {
		t.Fatal("event with larger ID must be after a zero-timestamp cursor with smaller ID")
	}
}

func TestRunSourceOfTruthSyncHaltsOnUnrecoverableCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.cursor")
	if err := os.WriteFile(path, []byte(`{"timestamp":`), 0o600); err != nil {
		t.Fatal(err)
	}
	processor := &fakeBatchProcessor{}
	RunSourceOfTruthSync(context.Background(), config.CollectorConfig{
		SourceOfTruth: true,
		URL:           "http://127.0.0.1:1",
		Collector:     "test",
		CursorPath:    path,
	}, processor)
	if ids := processor.FlattenedIDs(); len(ids) != 0 {
		t.Fatalf("corrupt cursor must halt before replay, processed %v", ids)
	}
}

func TestSortAndDedupeEventsKeepsDistinctEventsWithSameID(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := &models.Event{
		ID: "shared-id", EventID: "shared-id", Timestamp: ts,
		Service: "checkout", Event: "charge", Raw: map[string]any{"seq": "one"},
	}
	second := &models.Event{
		ID: "shared-id", EventID: "shared-id", Timestamp: ts,
		Service: "payments", Event: "refund", Raw: map[string]any{"seq": "two"},
	}

	got := sortAndDedupeEvents([]*models.Event{second, first}, collectorbridge.Cursor{})
	if len(got) != 2 {
		t.Fatalf("expected both distinct events sharing an ID to be kept, got %d: %+v", len(got), got)
	}
	services := map[string]bool{}
	for _, event := range got {
		services[event.Service] = true
	}
	if !services["checkout"] || !services["payments"] {
		t.Fatalf("expected both distinct payloads to survive dedupe, got %v", services)
	}
}

func TestSortAndDedupeEventsKeepsDistinctEmptyIDEventsWithSameTimestamp(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := &models.Event{Timestamp: ts, Service: "alpha", Raw: map[string]any{"seq": "one"}}
	second := &models.Event{Timestamp: ts, Service: "beta", Raw: map[string]any{"seq": "two"}}

	got := sortAndDedupeEvents([]*models.Event{first, second}, collectorbridge.Cursor{})
	if len(got) != 2 {
		t.Fatalf("expected both empty-ID events at the same timestamp to be kept, got %d: %+v", len(got), got)
	}
}

func TestSortAndDedupeEventsStillCollapsesExactDuplicates(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	original := &models.Event{
		ID: "evt-1", EventID: "evt-1", Timestamp: ts,
		Service: "checkout", Event: "charge", Raw: map[string]any{"seq": "one"},
	}
	duplicate := &models.Event{
		ID: "evt-1", EventID: "evt-1", Timestamp: ts,
		Service: "checkout", Event: "charge", Raw: map[string]any{"seq": "one"},
	}

	got := sortAndDedupeEvents([]*models.Event{original, duplicate}, collectorbridge.Cursor{})
	if len(got) != 1 {
		t.Fatalf("expected exact duplicates to collapse to one event, got %d: %+v", len(got), got)
	}
}
