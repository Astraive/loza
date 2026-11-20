package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// Minimal in-memory stores used to exercise the GraphQL server's configured
// processor end to end without a real database backend.
type redactionTestEventStore struct {
	saved []*models.Event
}

func (f *redactionTestEventStore) Save(_ context.Context, event *models.Event, _ *storage.LifecycleData) error {
	f.saved = append(f.saved, event)
	return nil
}

func (f *redactionTestEventStore) SaveBatch(_ context.Context, events []*models.Event, _ []*storage.LifecycleData) error {
	f.saved = append(f.saved, events...)
	return nil
}

func (f *redactionTestEventStore) Get(context.Context, string) (*models.Event, error) {
	return nil, errors.New("not found")
}

func (f *redactionTestEventStore) List(context.Context, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByTraceID(context.Context, string) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByIncidentID(context.Context, string) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByService(context.Context, string, string, string) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByEventName(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByOutcome(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByLevel(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByDurationRange(context.Context, float64, float64, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByEnvironment(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) FindByRelease(context.Context, string, int, int) ([]*models.Event, error) {
	return nil, nil
}

func (f *redactionTestEventStore) AverageDuration(context.Context, string, time.Time, time.Time) (float64, error) {
	return 0, nil
}

func (f *redactionTestEventStore) CountByEventName(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

func (f *redactionTestEventStore) CountByOutcome(context.Context, string, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

func (f *redactionTestEventStore) PercentileDuration(context.Context, string, float64, time.Time, time.Time) (float64, error) {
	return 0, nil
}

func (f *redactionTestEventStore) DistinctServices(context.Context) ([]string, error) {
	return nil, nil
}

func (f *redactionTestEventStore) DistinctEventNames(context.Context) ([]string, error) {
	return nil, nil
}

func (f *redactionTestEventStore) ListLifecycleSummaries(context.Context, *storage.LifecycleFilter) ([]*models.LifecycleSummary, int, error) {
	return nil, 0, nil
}

type redactionTestTopologyStore struct{}

func (*redactionTestTopologyStore) SaveAlias(context.Context, *models.ServiceAlias) error { return nil }

func (*redactionTestTopologyStore) GetAlias(context.Context, string, string) (*models.ServiceAlias, error) {
	return nil, errors.New("not found")
}

func (*redactionTestTopologyStore) GetHistory(context.Context, string) ([]*models.ServiceAlias, error) {
	return nil, nil
}

type redactionTestGraphStore struct{}

func (*redactionTestGraphStore) SaveNode(context.Context, *models.Node) error { return nil }
func (*redactionTestGraphStore) GetNode(context.Context, string) (*models.Node, error) {
	return nil, errors.New("not found")
}
func (*redactionTestGraphStore) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}
func (*redactionTestGraphStore) SaveEdge(context.Context, *models.Edge) error { return nil }
func (*redactionTestGraphStore) GetEdges(context.Context, string, string) ([]*models.Edge, error) {
	return nil, nil
}
func (*redactionTestGraphStore) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

type redactionTestStorage struct {
	events     *redactionTestEventStore
	topology   *redactionTestTopologyStore
	graph      *redactionTestGraphStore
	signatures storage.SignatureStore
}

func (*redactionTestStorage) Init(context.Context) error { return nil }
func (*redactionTestStorage) Close() error               { return nil }
func (s *redactionTestStorage) Events() storage.EventStore {
	return s.events
}
func (s *redactionTestStorage) Topology() storage.TopologyStore {
	return s.topology
}
func (s *redactionTestStorage) Graph() storage.GraphStore {
	return s.graph
}
func (*redactionTestStorage) Incidents() storage.IncidentStore { return nil }
func (s *redactionTestStorage) Signatures() storage.SignatureStore {
	return s.signatures
}
func (*redactionTestStorage) Remediations() storage.RemediationStore { return nil }
func (*redactionTestStorage) Feedback() storage.FeedbackStore        { return nil }

func redactionTestConfig() *config.Config {
	cfg, err := config.Default()
	if err != nil {
		panic(err)
	}
	cfg.PIIRedaction.Enabled = true
	cfg.PIIRedaction.Mode = "enforce"
	return cfg
}

func newRedactionTestStorage(events *redactionTestEventStore) *redactionTestStorage {
	return &redactionTestStorage{
		events:     events,
		topology:   &redactionTestTopologyStore{},
		graph:      &redactionTestGraphStore{},
		signatures: &graphQLSignatureStore{signatures: map[string]*models.IncidentSignature{}},
	}
}

// assertPIIRedacted processes one PII-bearing event through the processor that
// a given ingestion path uses and asserts the persisted event is redacted.
func assertPIIRedacted(t *testing.T, events *redactionTestEventStore, proc interface {
	ProcessEvent(context.Context, *models.Event) error
}) {
	t.Helper()
	event := &models.Event{
		ID:         "evt-redaction",
		Timestamp:  time.Now(),
		Kind:       models.EventKindLog,
		Service:    "api",
		Provenance: "loza",
		Raw: map[string]any{
			"email":   "alice@example.com",
			"api_key": "super-secret",
			"message": "plain text",
		},
	}
	if err := proc.ProcessEvent(context.Background(), event); err != nil {
		t.Fatalf("process event: %v", err)
	}
	if len(events.saved) != 1 {
		t.Fatalf("expected one saved event, got %d", len(events.saved))
	}
	raw := events.saved[0].Raw
	if got := raw["email"]; got != "[REDACTED]" {
		t.Fatalf("email not redacted: %v", got)
	}
	if got := raw["api_key"]; got != "[REDACTED]" {
		t.Fatalf("api_key not redacted: %v", got)
	}
	if got := raw["message"]; got != "plain text" {
		t.Fatalf("non-sensitive value unexpectedly changed: %v", got)
	}
}

func TestHTTPServerAppliesPIIRedaction(t *testing.T) {
	events := &redactionTestEventStore{}
	srv := NewServer(redactionTestConfig(), newRedactionTestStorage(events))
	assertPIIRedacted(t, events, srv.processor)
}

func TestGRPCServerAppliesPIIRedaction(t *testing.T) {
	events := &redactionTestEventStore{}
	srv := NewGRPCServer(redactionTestConfig(), newRedactionTestStorage(events))
	assertPIIRedacted(t, events, srv.processor)
}

func TestGraphQLServerAppliesPIIRedaction(t *testing.T) {
	events := &redactionTestEventStore{}
	// NewGraphQLServer wires the shared redactor-applying factory into the
	// processor used by every GraphQL ingest mutation.
	srv := NewGraphQLServer(redactionTestConfig(), newRedactionTestStorage(events))
	assertPIIRedacted(t, events, srv.processor)
}
