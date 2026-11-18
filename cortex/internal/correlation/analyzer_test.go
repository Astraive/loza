package correlation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
)

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
