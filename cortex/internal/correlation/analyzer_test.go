package correlation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// stubEventStore embeds the interface and overrides only List, which is the
// sole EventStore method analyze() calls.
type stubEventStore struct {
	storage.EventStore
	events []*models.Event
}

func (s *stubEventStore) List(context.Context, int, int) ([]*models.Event, error) {
	return s.events, nil
}

// capturingGraphStore records every edge saved by the analyzer. All GraphStore
// methods are implemented explicitly (rather than relying on an embedded,
// otherwise-nil interface) so every synthesis path can be exercised safely.
type capturingGraphStore struct {
	edges []*models.Edge
}

func (s *capturingGraphStore) SaveNode(context.Context, *models.Node) error { return nil }
func (s *capturingGraphStore) GetNode(context.Context, string) (*models.Node, error) {
	return nil, nil
}
func (s *capturingGraphStore) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}
func (s *capturingGraphStore) SaveEdge(_ context.Context, e *models.Edge) error {
	s.edges = append(s.edges, e)
	return nil
}
func (s *capturingGraphStore) GetEdges(context.Context, string, string) ([]*models.Edge, error) {
	return nil, nil
}
func (s *capturingGraphStore) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

type failingGraphStore struct {
	err      error
	attempts int
}

func (s *failingGraphStore) SaveNode(context.Context, *models.Node) error { return nil }
func (s *failingGraphStore) GetNode(context.Context, string) (*models.Node, error) {
	return nil, nil
}
func (s *failingGraphStore) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}
func (s *failingGraphStore) SaveEdge(context.Context, *models.Edge) error {
	s.attempts++
	return s.err
}
func (s *failingGraphStore) GetEdges(context.Context, string, string) ([]*models.Edge, error) {
	return nil, nil
}
func (s *failingGraphStore) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

// pagedEventStore serves offset-based pages from an in-memory slice. Embedding
// the interface satisfies the unrelated EventStore methods that analysis never
// calls, while List is overridden to mimic the real stores' pagination.
type pagedEventStore struct {
	storage.EventStore
	events  []*models.Event
	limits  []int
	offsets []int
}

func (s *pagedEventStore) List(_ context.Context, limit, offset int) ([]*models.Event, error) {
	s.limits = append(s.limits, limit)
	s.offsets = append(s.offsets, offset)
	if offset >= len(s.events) {
		return nil, nil
	}
	end := offset + limit
	if end > len(s.events) {
		end = len(s.events)
	}
	return s.events[offset:end], nil
}

type recordingGraphStore struct {
	storage.GraphStore
	edges []*models.Edge
}

func (s *recordingGraphStore) SaveEdge(_ context.Context, edge *models.Edge) error {
	s.edges = append(s.edges, edge)
	return nil
}

func TestAnalyzeScansEventsBeyondFirstPage(t *testing.T) {
	// The old implementation issued a single List(ctx, 10000, 0) call. Place the
	// two correlated anomalies at indexes 10000 and 10003: with the old code
	// neither is fetched, so no co_occurred edge is produced.
	const (
		pageSize  = 1000
		maxEvents = 10005
		oldCap    = 10000
	)
	timestamp := time.Now().Add(-time.Minute)
	events := make([]*models.Event, maxEvents)
	for i := range events {
		events[i] = &models.Event{
			ID:        fmt.Sprintf("filler-%d", i),
			Service:   "worker",
			Kind:      models.EventKindLog,
			Timestamp: timestamp,
			Raw:       map[string]any{"level": "info"},
		}
	}
	events[oldCap] = &models.Event{
		ID: "anomaly-a", Service: "api", Kind: models.EventKindIncidentSignal, Timestamp: timestamp,
	}
	events[oldCap+3] = &models.Event{
		ID: "anomaly-b", Service: "db", Kind: models.EventKindIncidentSignal, Timestamp: timestamp,
	}

	store := &pagedEventStore{events: events}
	graph := &recordingGraphStore{}
	cfg := DefaultConfig()
	cfg.ListPageSize = pageSize
	cfg.MaxEvents = maxEvents
	analyzer := NewAnalyzer(cfg, store, graph)

	if err := analyzer.analyze(context.Background()); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.offsets) <= 1 {
		t.Fatalf("expected paginated List calls, got offsets %v", store.offsets)
	}
	var reachedOldCap bool
	for _, offset := range store.offsets {
		if offset >= oldCap {
			reachedOldCap = true
		}
	}
	if !reachedOldCap {
		t.Fatalf("expected a List call at or beyond offset %d, got %v", oldCap, store.offsets)
	}

	var found bool
	for _, edge := range graph.edges {
		if edge.Type != models.EdgeTypeCoOccurred {
			continue
		}
		if (edge.FromNodeID == "anomaly-a" && edge.ToNodeID == "anomaly-b") ||
			(edge.FromNodeID == "anomaly-b" && edge.ToNodeID == "anomaly-a") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected co_occurred edge for anomalies past the old %d-event cap, got %d edges", oldCap, len(graph.edges))
	}
}

func TestAnalyzeRespectsConfiguredPaginationLimits(t *testing.T) {
	now := time.Now()
	events := make([]*models.Event, 20)
	for i := range events {
		events[i] = &models.Event{ID: fmt.Sprintf("event-%d", i), Service: "svc", Timestamp: now}
	}
	store := &pagedEventStore{events: events}
	cfg := DefaultConfig()
	cfg.ListPageSize = 2
	cfg.MaxEvents = 5
	analyzer := NewAnalyzer(cfg, store, &recordingGraphStore{})

	if err := analyzer.analyze(context.Background()); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if want := []int{0, 2, 4}; !reflect.DeepEqual(store.offsets, want) {
		t.Fatalf("offsets = %v, want %v", store.offsets, want)
	}
	// The final request is clamped to the remaining allowance.
	if want := []int{2, 2, 1}; !reflect.DeepEqual(store.limits, want) {
		t.Fatalf("limits = %v, want %v", store.limits, want)
	}
}

func TestAnalyzeFallsBackToDefaultLimitsForNonPositiveConfig(t *testing.T) {
	store := &pagedEventStore{events: []*models.Event{
		{ID: "event-0", Service: "svc", Timestamp: time.Now()},
	}}
	cfg := DefaultConfig()
	cfg.ListPageSize = 0
	cfg.MaxEvents = -1
	analyzer := NewAnalyzer(cfg, store, &recordingGraphStore{})

	if err := analyzer.analyze(context.Background()); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.limits) != 1 || store.limits[0] != defaultListPageSize {
		t.Fatalf("expected fallback page size %d, got limits %v", defaultListPageSize, store.limits)
	}
}

// emptyEventStore overrides only List; the analyzer analysis path needs no
// other event-store methods.
type emptyEventStore struct {
	storage.EventStore
}

func (emptyEventStore) List(context.Context, int, int) ([]*models.Event, error) {
	return nil, nil
}

func TestAnalyzerStateTransitionsWithManagedContext(t *testing.T) {
	analyzer := NewAnalyzer(DefaultConfig(), emptyEventStore{}, &failingGraphStore{})
	if !analyzer.Enabled() {
		t.Fatal("expected analyzer to be enabled")
	}
	if got := analyzer.State(); got != StateStopped {
		t.Fatalf("new enabled analyzer state = %q, want %q", got, StateStopped)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		analyzer.Run(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for analyzer.State() != StateRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := analyzer.State(); got != StateRunning {
		t.Fatalf("running analyzer state = %q, want %q", got, StateRunning)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("analyzer did not stop after context cancellation")
	}
	if got := analyzer.State(); got != StateStopped {
		t.Fatalf("stopped analyzer state = %q, want %q", got, StateStopped)
	}
}

func TestDisabledAnalyzerReportsDisabledAndExitsImmediately(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	analyzer := NewAnalyzer(cfg, nil, nil)
	if analyzer.Enabled() {
		t.Fatal("expected analyzer to be disabled")
	}
	if got := analyzer.State(); got != StateDisabled {
		t.Fatalf("disabled analyzer state = %q, want %q", got, StateDisabled)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	analyzer.Run(ctx) // Must return immediately.
	if got := analyzer.State(); got != StateDisabled {
		t.Fatalf("disabled analyzer state after Run = %q, want %q", got, StateDisabled)
	}
}

func TestSynthesizeCoOccurrenceReturnsPersistenceErrors(t *testing.T) {
	persistErr := errors.New("persist edge")
	graph := &failingGraphStore{err: persistErr}
	analyzer := NewAnalyzer(DefaultConfig(), nil, graph)
	now := time.Now()
	events := []*models.Event{
		{ID: "evt-a", Service: "api", Kind: models.EventKindLog, Timestamp: now, Raw: map[string]any{"level": "error"}},
		{ID: "evt-b", Service: "db", Kind: models.EventKindLog, Timestamp: now, Raw: map[string]any{"level": "error"}},
	}

	err := analyzer.synthesizeCoOccurrence(context.Background(), events)
	if !errors.Is(err, persistErr) {
		t.Fatalf("expected persistence error, got %v", err)
	}
	if graph.attempts != 3 {
		t.Fatalf("expected three idempotent save attempts, got %d", graph.attempts)
	}
}

// Regression for MASS-0300: two anomalous events from different services that
// fall within the configured sliding window but straddle a 1-minute boundary
// must still be correlated. The old fixed-bucket implementation skipped them.
func TestSynthesizeCoOccurrenceCrossesMinuteBoundaryWithinWindow(t *testing.T) {
	graph := &capturingGraphStore{}
	analyzer := NewAnalyzer(DefaultConfig(), nil, graph)

	start := time.Date(2026, time.January, 2, 3, 4, 59, 0, time.UTC) // 03:04:59
	events := []*models.Event{
		{ID: "evt-a", Service: "api", Kind: models.EventKindLog, Timestamp: start, Raw: map[string]any{"level": "error"}},
		{ID: "evt-b", Service: "db", Kind: models.EventKindLog, Timestamp: start.Add(2 * time.Second), Raw: map[string]any{"level": "error"}}, // 03:05:01
	}

	if err := analyzer.synthesizeCoOccurrence(context.Background(), events); err != nil {
		t.Fatalf("synthesizeCoOccurrence returned error: %v", err)
	}
	if len(graph.edges) != 1 {
		t.Fatalf("expected 1 co-occurrence edge across the minute boundary, got %d", len(graph.edges))
	}
	edge := graph.edges[0]
	if edge.Type != models.EdgeTypeCoOccurred {
		t.Fatalf("expected co_occurred edge, got %s", edge.Type)
	}
	connected := (edge.FromNodeID == "evt-a" && edge.ToNodeID == "evt-b") ||
		(edge.FromNodeID == "evt-b" && edge.ToNodeID == "evt-a")
	if !connected {
		t.Fatalf("expected edge between evt-a and evt-b, got %s -> %s", edge.FromNodeID, edge.ToNodeID)
	}
}

// The sliding window is still an upper bound: anomalous events further apart
// than CoOccurrenceWindow must not be correlated, even when in the same bucket.
func TestSynthesizeCoOccurrenceIgnoresEventsOutsideWindow(t *testing.T) {
	cfg := DefaultConfig()
	graph := &capturingGraphStore{}
	analyzer := NewAnalyzer(cfg, nil, graph)

	start := time.Date(2026, time.January, 2, 3, 0, 0, 0, time.UTC)
	events := []*models.Event{
		{ID: "evt-a", Service: "api", Kind: models.EventKindIncidentSignal, Timestamp: start},
		{ID: "evt-b", Service: "db", Kind: models.EventKindIncidentSignal, Timestamp: start.Add(cfg.CoOccurrenceWindow + time.Second)},
	}

	if err := analyzer.synthesizeCoOccurrence(context.Background(), events); err != nil {
		t.Fatalf("synthesizeCoOccurrence returned error: %v", err)
	}
	if len(graph.edges) != 0 {
		t.Fatalf("expected no edge for events outside the window, got %d", len(graph.edges))
	}
}

// TestAnalyzeUsesDeploymentAdjacencyWindow reproduces MASS-0299: a deploy
// older than CoOccurrenceWindow*2 (10 min) but inside DeploymentAdjacencyWindow
// (30 min) must still produce a deployment_adjacent edge. Before the fix the
// deploy was pruned by analyze() before synthesizeDeploymentAdjacency ran.
func TestAnalyzeUsesDeploymentAdjacencyWindow(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	events := []*models.Event{
		// 15 minutes old: beyond the 10-minute co-occurrence cutoff, but
		// inside the 30-minute deployment adjacency window.
		{ID: "deploy-1", Service: "api", Kind: models.EventKindDeploy, Timestamp: now.Add(-15 * time.Minute)},
		// 5 minutes old on a different service: 10 minutes after the deploy,
		// also inside the deployment adjacency window.
		{ID: "anomaly-1", Service: "db", Kind: models.EventKindIncidentSignal, Timestamp: now.Add(-5 * time.Minute)},
	}

	graph := &capturingGraphStore{}
	analyzer := NewAnalyzer(cfg, &stubEventStore{events: events}, graph)
	if err := analyzer.analyze(context.Background()); err != nil {
		t.Fatalf("analyze returned error: %v", err)
	}

	for _, edge := range graph.edges {
		if edge.Type == models.EdgeTypeDeploymentAdjacent &&
			edge.FromNodeID == "deploy-1" && edge.ToNodeID == "anomaly-1" {
			return
		}
	}
	t.Fatalf("expected a deployment_adjacent edge from deploy-1 to anomaly-1, got %d edges: %+v", len(graph.edges), graph.edges)
}
