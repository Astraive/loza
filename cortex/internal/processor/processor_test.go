package processor

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

type fakeEventStore struct {
	saved         []*models.Event
	batchSaved    [][]*models.Event
	byTraceID     map[string][]*models.Event
	byIncident    map[string][]*models.Event
	traceCalls    int
	incidentCalls int
	traceErr      error
	incidentErr   error
}

func (f *fakeEventStore) Save(_ context.Context, event *models.Event, _ *storage.LifecycleData) error {
	f.saved = append(f.saved, event)
	return nil
}

func (f *fakeEventStore) SaveBatch(_ context.Context, events []*models.Event, _ []*storage.LifecycleData) error {
	batch := make([]*models.Event, len(events))
	copy(batch, events)
	f.batchSaved = append(f.batchSaved, batch)
	return nil
}

func (f *fakeEventStore) Get(_ context.Context, id string) (*models.Event, error) {
	return nil, errors.New("not found")
}

func (f *fakeEventStore) List(context.Context, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByTraceID(_ context.Context, traceID string) ([]*models.Event, error) {
	f.traceCalls++
	if f.traceErr != nil {
		return nil, f.traceErr
	}
	return f.byTraceID[traceID], nil
}

func (f *fakeEventStore) FindByIncidentID(_ context.Context, incidentID string) ([]*models.Event, error) {
	f.incidentCalls++
	if f.incidentErr != nil {
		return nil, f.incidentErr
	}
	return f.byIncident[incidentID], nil
}

func (f *fakeEventStore) FindByService(context.Context, string, string, string) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByEventName(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByOutcome(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByLevel(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByDurationRange(context.Context, float64, float64, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByEnvironment(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) FindByRelease(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *fakeEventStore) AverageDuration(context.Context, string, time.Time, time.Time) (float64, error) {
	return 0, nil
}

func (f *fakeEventStore) CountByEventName(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

func (f *fakeEventStore) CountByOutcome(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

func (f *fakeEventStore) PercentileDuration(context.Context, string, float64, time.Time, time.Time) (float64, error) {
	return 0, nil
}

func (f *fakeEventStore) DistinctServices(context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeEventStore) DistinctEventNames(context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeEventStore) ListLifecycleSummaries(context.Context, *storage.LifecycleFilter) ([]*models.LifecycleSummary, int, error) {
	return nil, 0, nil
}

type fakeTopologyStoreForProcessor struct {
	alias *models.ServiceAlias
}

func (f *fakeTopologyStoreForProcessor) SaveAlias(context.Context, *models.ServiceAlias) error {
	return nil
}

func (f *fakeTopologyStoreForProcessor) GetAlias(_ context.Context, alias, timestamp string) (*models.ServiceAlias, error) {
	if f.alias != nil && f.alias.Alias == alias {
		return f.alias, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeTopologyStoreForProcessor) GetHistory(context.Context, string) ([]*models.ServiceAlias, error) {
	return nil, nil
}

type fakeGraphStoreForProcessor struct {
	nodes      []*models.Node
	edges      []*models.Edge
	saveEdgeFn func(*models.Edge) error
}

func (f *fakeGraphStoreForProcessor) SaveNode(_ context.Context, node *models.Node) error {
	f.nodes = append(f.nodes, node)
	return nil
}

func (f *fakeGraphStoreForProcessor) GetNode(context.Context, string) (*models.Node, error) {
	return nil, errors.New("not found")
}

func (f *fakeGraphStoreForProcessor) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}

func (f *fakeGraphStoreForProcessor) SaveEdge(_ context.Context, edge *models.Edge) error {
	if f.saveEdgeFn != nil {
		if err := f.saveEdgeFn(edge); err != nil {
			return err
		}
	}
	f.edges = append(f.edges, edge)
	return nil
}

func (f *fakeGraphStoreForProcessor) GetEdges(context.Context, string, string) ([]*models.Edge, error) {
	return nil, nil
}

func (f *fakeGraphStoreForProcessor) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

func TestProcessEventCreatesGraphNodes(t *testing.T) {
	events := &fakeEventStore{}
	topology := &fakeTopologyStoreForProcessor{
		alias: &models.ServiceAlias{Alias: "svc-a", Canonical: "svc-b"},
	}
	graphStore := &fakeGraphStoreForProcessor{}
	p := NewEventProcessor(events, topology, graphStore)

	event := &models.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       models.EventKindDeploy,
		Service:    "svc-a",
		Provenance: "jsonl",
		Raw: map[string]any{
			"message":  "deploy failed",
			"secret":   "should-not-be-copied",
			"version":  "1.2.3",
			"trace_id": "trace-1",
		},
	}
	if err := p.ProcessEvent(context.Background(), event); err != nil {
		t.Fatalf("process event: %v", err)
	}
	if len(events.saved) != 1 || events.saved[0].CreatedAt.IsZero() {
		t.Fatalf("expected normalized saved event, got %+v", events.saved)
	}
	if len(graphStore.nodes) != 3 {
		t.Fatalf("expected service, event, and deploy nodes, got %d", len(graphStore.nodes))
	}
	if len(graphStore.edges) != 2 {
		t.Fatalf("expected event-service and deploy-service edges, got %d", len(graphStore.edges))
	}
	if _, ok := graphStore.nodes[1].Attributes["secret"]; ok {
		t.Fatalf("expected graph node attrs to avoid copying raw secrets, got %+v", graphStore.nodes[1].Attributes)
	}
	if graphStore.nodes[1].Attributes["message"] != "deploy failed" {
		t.Fatalf("expected graph node attrs to keep derived message, got %+v", graphStore.nodes[1].Attributes)
	}
}

func TestProcessJSONLDefaultsProvenance(t *testing.T) {
	events := &fakeEventStore{}
	topology := &fakeTopologyStoreForProcessor{}
	graphStore := &fakeGraphStoreForProcessor{}
	p := NewEventProcessor(events, topology, graphStore)

	payload := bytes.NewBufferString(`{"id":"evt-2","timestamp":"2026-01-01T00:00:00Z","kind":"log","service":"svc-a"}` + "\n")
	if err := p.ProcessJSONL(context.Background(), payload); err != nil {
		t.Fatalf("process jsonl: %v", err)
	}
	if len(events.batchSaved) != 1 || len(events.batchSaved[0]) != 1 {
		t.Fatalf("expected one batch event, got %+v", events.batchSaved)
	}
	if got := events.batchSaved[0][0].Provenance; got != "jsonl" {
		t.Fatalf("expected jsonl provenance, got %s", got)
	}
}

func TestProcessBatchLinksLocalTrace(t *testing.T) {
	events := &fakeEventStore{}
	topology := &fakeTopologyStoreForProcessor{}
	graphStore := &fakeGraphStoreForProcessor{}
	p := NewEventProcessor(events, topology, graphStore)

	ts := time.Now()
	batch := []*models.Event{
		{
			ID:         "evt-1",
			Timestamp:  ts,
			Kind:       models.EventKindLog,
			Service:    "svc-a",
			TraceID:    "trace-1",
			Provenance: "jsonl",
		},
		{
			ID:         "evt-2",
			Timestamp:  ts.Add(time.Second),
			Kind:       models.EventKindMetric,
			Service:    "svc-a",
			TraceID:    "trace-1",
			Provenance: "jsonl",
		},
	}

	if err := p.ProcessBatch(context.Background(), batch); err != nil {
		t.Fatalf("process batch: %v", err)
	}
	// External trace events must be queried even when multiple batch events share
	// the trace, so pre-existing events connected to the trace are not omitted.
	if events.traceCalls != 1 {
		t.Fatalf("expected one trace lookup for the shared-trace group, got %d", events.traceCalls)
	}

	foundLocalTraceEdge := false
	for _, edge := range graphStore.edges {
		if edge.Type == models.EdgeTypeSameTrace &&
			((edge.FromNodeID == "evt-1" && edge.ToNodeID == "evt-2") ||
				(edge.FromNodeID == "evt-2" && edge.ToNodeID == "evt-1")) {
			foundLocalTraceEdge = true
			break
		}
	}
	if !foundLocalTraceEdge {
		t.Fatalf("expected same-trace edge between local batch events, got %+v", graphStore.edges)
	}
}

func TestProcessBatchLinksExternalTraceEventsWithMultipleLocalEvents(t *testing.T) {
	external := &models.Event{
		ID:         "external-1",
		Timestamp:  time.Now().Add(-time.Hour),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		TraceID:    "trace-1",
		Provenance: "jsonl",
	}
	events := &fakeEventStore{
		byTraceID: map[string][]*models.Event{
			"trace-1": {external},
		},
	}
	topology := &fakeTopologyStoreForProcessor{}
	graphStore := &fakeGraphStoreForProcessor{}
	p := NewEventProcessor(events, topology, graphStore)

	ts := time.Now()
	batch := []*models.Event{
		{
			ID:         "evt-1",
			Timestamp:  ts,
			Kind:       models.EventKindLog,
			Service:    "svc-a",
			TraceID:    "trace-1",
			Provenance: "jsonl",
		},
		{
			ID:         "evt-2",
			Timestamp:  ts.Add(time.Second),
			Kind:       models.EventKindMetric,
			Service:    "svc-a",
			TraceID:    "trace-1",
			Provenance: "jsonl",
		},
	}

	if err := p.ProcessBatch(context.Background(), batch); err != nil {
		t.Fatalf("process batch: %v", err)
	}

	connected := map[string]bool{}
	for _, edge := range graphStore.edges {
		if edge.Type != models.EdgeTypeSameTrace {
			continue
		}
		for _, localID := range []string{"evt-1", "evt-2"} {
			if (edge.FromNodeID == localID && edge.ToNodeID == external.ID) ||
				(edge.ToNodeID == localID && edge.FromNodeID == external.ID) {
				connected[localID] = true
			}
		}
	}
	for _, localID := range []string{"evt-1", "evt-2"} {
		if !connected[localID] {
			t.Fatalf("expected external trace event to be linked to %s, got edges %+v", localID, graphStore.edges)
		}
	}
}

func TestProcessBatchLinksExternalIncidentEventsWithMultipleLocalEvents(t *testing.T) {
	external := &models.Event{
		ID:         "external-incident-1",
		Timestamp:  time.Now().Add(-time.Hour),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		IncidentID: "incident-1",
		Provenance: "jsonl",
	}
	events := &fakeEventStore{
		byIncident: map[string][]*models.Event{
			"incident-1": {external},
		},
	}
	topology := &fakeTopologyStoreForProcessor{}
	graphStore := &fakeGraphStoreForProcessor{}
	p := NewEventProcessor(events, topology, graphStore)

	ts := time.Now()
	batch := []*models.Event{
		{
			ID:         "evt-1",
			Timestamp:  ts,
			Kind:       models.EventKindLog,
			Service:    "svc-a",
			IncidentID: "incident-1",
			Provenance: "jsonl",
		},
		{
			ID:         "evt-2",
			Timestamp:  ts.Add(time.Second),
			Kind:       models.EventKindMetric,
			Service:    "svc-a",
			IncidentID: "incident-1",
			Provenance: "jsonl",
		},
	}

	if err := p.ProcessBatch(context.Background(), batch); err != nil {
		t.Fatalf("process batch: %v", err)
	}
	if events.incidentCalls != 1 {
		t.Fatalf("expected one incident lookup for the shared-incident group, got %d", events.incidentCalls)
	}

	connected := map[string]bool{}
	for _, edge := range graphStore.edges {
		if edge.Type != models.EdgeTypeSameIncident {
			continue
		}
		for _, localID := range []string{"evt-1", "evt-2"} {
			if (edge.FromNodeID == localID && edge.ToNodeID == external.ID) ||
				(edge.ToNodeID == localID && edge.FromNodeID == external.ID) {
				connected[localID] = true
			}
		}
	}
	for _, localID := range []string{"evt-1", "evt-2"} {
		if !connected[localID] {
			t.Fatalf("expected external incident event to be linked to %s, got edges %+v", localID, graphStore.edges)
		}
	}
}

func TestProcessEventReturnsAggregatedLinkEdgeSaveErrors(t *testing.T) {
	errLinkA := errors.New("link A save failed")
	errLinkB := errors.New("link B save failed")
	events := &fakeEventStore{}
	topology := &fakeTopologyStoreForProcessor{}
	graphStore := &fakeGraphStoreForProcessor{
		saveEdgeFn: func(edge *models.Edge) error {
			switch edge.ToNodeID {
			case "target-a":
				return errLinkA
			case "target-b":
				return errLinkB
			default:
				return nil
			}
		},
	}
	p := NewEventProcessor(events, topology, graphStore)

	event := &models.Event{
		ID:         "evt-links",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		Provenance: "jsonl",
		Links: []*models.EventLink{
			{Type: "co_occurred", Target: "target-a"},
			{Type: "co_occurred", Target: "target-b"},
		},
	}

	err := p.ProcessEvent(context.Background(), event)
	if err == nil {
		t.Fatal("expected ProcessEvent to fail when link edges cannot be persisted")
	}
	if !errors.Is(err, errLinkA) || !errors.Is(err, errLinkB) {
		t.Fatalf("expected both link edge failures to be reported, got %v", err)
	}
}

func TestProcessEventReturnsTraceEdgeSaveError(t *testing.T) {
	errTraceEdge := errors.New("trace edge save failed")
	events := &fakeEventStore{
		byTraceID: map[string][]*models.Event{
			"trace-1": {{ID: "evt-related", Timestamp: time.Now(), Kind: models.EventKindLog, Service: "svc-a"}},
		},
	}
	graphStore := &fakeGraphStoreForProcessor{
		saveEdgeFn: func(edge *models.Edge) error {
			if edge.Type == models.EdgeTypeSameTrace {
				return errTraceEdge
			}
			return nil
		},
	}
	p := NewEventProcessor(events, &fakeTopologyStoreForProcessor{}, graphStore)

	event := &models.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		TraceID:    "trace-1",
		Provenance: "jsonl",
	}

	if err := p.ProcessEvent(context.Background(), event); !errors.Is(err, errTraceEdge) {
		t.Fatalf("expected trace edge save failure to be returned, got %v", err)
	}
}

func TestProcessEventReturnsIncidentEdgeSaveError(t *testing.T) {
	errIncidentEdge := errors.New("incident edge save failed")
	events := &fakeEventStore{
		byIncident: map[string][]*models.Event{
			"inc-1": {{ID: "evt-related", Timestamp: time.Now(), Kind: models.EventKindLog, Service: "svc-a"}},
		},
	}
	graphStore := &fakeGraphStoreForProcessor{
		saveEdgeFn: func(edge *models.Edge) error {
			if edge.Type == models.EdgeTypeSameIncident {
				return errIncidentEdge
			}
			return nil
		},
	}
	p := NewEventProcessor(events, &fakeTopologyStoreForProcessor{}, graphStore)

	event := &models.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		IncidentID: "inc-1",
		Provenance: "jsonl",
	}

	if err := p.ProcessEvent(context.Background(), event); !errors.Is(err, errIncidentEdge) {
		t.Fatalf("expected incident edge save failure to be returned, got %v", err)
	}
}

func TestProcessEventReturnsTraceLookupError(t *testing.T) {
	errLookup := errors.New("trace store unavailable")
	events := &fakeEventStore{traceErr: errLookup}
	p := NewEventProcessor(events, &fakeTopologyStoreForProcessor{}, &fakeGraphStoreForProcessor{})

	event := &models.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		TraceID:    "trace-1",
		Provenance: "jsonl",
	}

	if err := p.ProcessEvent(context.Background(), event); !errors.Is(err, errLookup) {
		t.Fatalf("expected trace lookup failure to be returned, got %v", err)
	}
}

func TestProcessEventReturnsIncidentLookupError(t *testing.T) {
	errLookup := errors.New("incident store unavailable")
	events := &fakeEventStore{incidentErr: errLookup}
	p := NewEventProcessor(events, &fakeTopologyStoreForProcessor{}, &fakeGraphStoreForProcessor{})

	event := &models.Event{
		ID:         "evt-1",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		IncidentID: "inc-1",
		Provenance: "jsonl",
	}

	if err := p.ProcessEvent(context.Background(), event); !errors.Is(err, errLookup) {
		t.Fatalf("expected incident lookup failure to be returned, got %v", err)
	}
}

func TestProcessBatchReturnsLinkEdgeSaveError(t *testing.T) {
	errLink := errors.New("batch link edge save failed")
	graphStore := &fakeGraphStoreForProcessor{
		saveEdgeFn: func(edge *models.Edge) error {
			if edge.Type == models.EdgeType(linkType) && edge.ToNodeID == "target-x" {
				return errLink
			}
			return nil
		},
	}
	p := NewEventProcessor(&fakeEventStore{}, &fakeTopologyStoreForProcessor{}, graphStore)

	batch := []*models.Event{{
		ID:         "evt-batch-link",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		Provenance: "jsonl",
		Links:      []*models.EventLink{{Type: linkType, Target: "target-x"}},
	}}

	if err := p.ProcessBatch(context.Background(), batch); !errors.Is(err, errLink) {
		t.Fatalf("expected batch link edge save failure to be returned, got %v", err)
	}
}

func TestProcessBatchReturnsIncidentLookupError(t *testing.T) {
	errLookup := errors.New("batch incident store unavailable")
	events := &fakeEventStore{incidentErr: errLookup}
	p := NewEventProcessor(events, &fakeTopologyStoreForProcessor{}, &fakeGraphStoreForProcessor{})

	batch := []*models.Event{{
		ID:         "evt-batch-incident",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "svc-a",
		IncidentID: "inc-1",
		Provenance: "jsonl",
	}}

	if err := p.ProcessBatch(context.Background(), batch); !errors.Is(err, errLookup) {
		t.Fatalf("expected batch incident lookup failure to be returned, got %v", err)
	}
}

const linkType = "co_occurred"
