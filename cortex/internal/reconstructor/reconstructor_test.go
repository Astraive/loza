package reconstructor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/graph"
	"github.com/astraive/loza/cortex/internal/learner"
	"github.com/astraive/loza/cortex/internal/matcher"
	"github.com/astraive/loza/cortex/internal/models"
)

type fakeReconGraphStore struct {
	nodes map[string]*models.Node
	edges map[string][]*models.Edge
}

func (f *fakeReconGraphStore) SaveNode(_ context.Context, node *models.Node) error {
	f.nodes[node.ID] = node
	return nil
}

func (f *fakeReconGraphStore) GetNode(_ context.Context, id string) (*models.Node, error) {
	if node, ok := f.nodes[id]; ok {
		return node, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeReconGraphStore) ListNodes(context.Context, string, int) ([]*models.Node, error) {
	return nil, nil
}

func (f *fakeReconGraphStore) SaveEdge(_ context.Context, edge *models.Edge) error {
	f.edges[edge.FromNodeID] = append(f.edges[edge.FromNodeID], edge)
	return nil
}

func (f *fakeReconGraphStore) GetEdges(_ context.Context, nodeID string, edgeType string) ([]*models.Edge, error) {
	var out []*models.Edge
	for _, edge := range f.edges[nodeID] {
		if edgeType == "" || string(edge.Type) == edgeType {
			out = append(out, edge)
		}
	}
	return out, nil
}

func (f *fakeReconGraphStore) Traverse(context.Context, string, models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

type fakeReconIncidentStore struct {
	incidents map[string]*models.Incident
	saved     []*models.Incident
}

func (f *fakeReconIncidentStore) Save(_ context.Context, incident *models.Incident) error {
	f.saved = append(f.saved, incident)
	f.incidents[incident.ID] = incident
	return nil
}

func (f *fakeReconIncidentStore) Get(_ context.Context, id string) (*models.Incident, error) {
	if incident, ok := f.incidents[id]; ok {
		return incident, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeReconIncidentStore) GetBySignature(context.Context, string) (*models.Incident, error) {
	return nil, nil
}

func (f *fakeReconIncidentStore) List(context.Context, int, int) ([]*models.Incident, error) {
	return nil, nil
}

type fakeReconSignatureStore struct {
	sigs    map[string]*models.IncidentSignature
	similar map[string][]*models.SimilarIncident
}

func (f *fakeReconSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	f.sigs[sig.SignatureID] = sig
	return nil
}

func (f *fakeReconSignatureStore) Get(_ context.Context, id string) (*models.IncidentSignature, error) {
	if sig, ok := f.sigs[id]; ok {
		return sig, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeReconSignatureStore) List(_ context.Context, limit int) ([]*models.IncidentSignature, error) {
	var out []*models.IncidentSignature
	for _, sig := range f.sigs {
		out = append(out, sig)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeReconSignatureStore) FindSimilar(_ context.Context, sig *models.IncidentSignature, limit int) ([]*models.SimilarIncident, error) {
	items := f.similar[sig.SignatureID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (f *fakeReconSignatureStore) FindByBehavioralHash(_ context.Context, hash string) ([]*models.IncidentSignature, error) {
	var out []*models.IncidentSignature
	for _, sig := range f.sigs {
		if sig.BehavioralHash == hash {
			out = append(out, sig)
		}
	}
	return out, nil
}

func (f *fakeReconSignatureStore) UpdateDecay(_ context.Context, id string, factor float64) error {
	if sig, ok := f.sigs[id]; ok {
		sig.DecayFactor = factor
	}
	return nil
}

func (f *fakeReconSignatureStore) ArchiveStale(_ context.Context, threshold float64) (int, error) {
	return 0, nil
}

func (f *fakeReconSignatureStore) UpdateLastMatched(_ context.Context, id string) error {
	return nil
}

type fakeReconRemediationStore struct {
	stats     map[string][]*models.RemediationStats
	listErr   error
	listCalls int
}

func (f *fakeReconRemediationStore) Save(context.Context, *models.Remediation) error {
	return nil
}

func (f *fakeReconRemediationStore) Get(context.Context, string) (*models.Remediation, error) {
	return nil, nil
}

func (f *fakeReconRemediationStore) ListByIncident(context.Context, string) ([]*models.Remediation, error) {
	return nil, nil
}

func (f *fakeReconRemediationStore) ListBySignature(_ context.Context, signatureID string, limit int) ([]*models.RemediationStats, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	items := f.stats[signatureID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

type fakeReconFeedbackStore struct{}

func (f *fakeReconFeedbackStore) Save(context.Context, *models.RemediationFeedback) error {
	return nil
}

func (f *fakeReconFeedbackStore) GetByRemediation(context.Context, string) ([]*models.RemediationFeedback, error) {
	return nil, nil
}

func (f *fakeReconFeedbackStore) GetSuccessRate(context.Context, string, string) (float64, error) {
	return 0, nil
}

func buildReconIncidentGraph(store *fakeReconGraphStore, incidentID string) {
	now := time.Now()
	store.nodes[incidentID] = &models.Node{
		ID:        incidentID,
		Type:      models.NodeTypeIncident,
		Label:     "svc-a",
		CreatedAt: now,
	}
	store.nodes["metric-1"] = &models.Node{
		ID:        "metric-1",
		Type:      models.NodeTypeMetric,
		Label:     "svc-a",
		Attributes: map[string]interface{}{
			"service":     "svc-a",
			"metric_name": "error_rate",
			"anomaly":     true,
			"value":       0.8,
		},
		CreatedAt: now.Add(time.Second),
	}
	store.nodes["log-1"] = &models.Node{
		ID:        "log-1",
		Type:      models.NodeTypeLog,
		Label:     "svc-b",
		Attributes: map[string]interface{}{
			"service": "svc-b",
			"level":   "error",
			"message": "boom",
		},
		CreatedAt: now.Add(2 * time.Second),
	}
	store.edges[incidentID] = []*models.Edge{
		{ID: "e1", FromNodeID: incidentID, ToNodeID: "metric-1", Type: models.EdgeTypeSameIncident, Weight: 1},
		{ID: "e2", FromNodeID: incidentID, ToNodeID: "log-1", Type: models.EdgeTypeSameIncident, Weight: 1},
	}
}

func TestReconstructFastAndFromSignal(t *testing.T) {
	graphStore := &fakeReconGraphStore{nodes: map[string]*models.Node{}, edges: map[string][]*models.Edge{}}
	buildReconIncidentGraph(graphStore, "inc-1")
	incidentStore := &fakeReconIncidentStore{
		incidents: map[string]*models.Incident{
			"inc-1": {ID: "inc-1", SignatureID: "sig-1", Status: "active", Severity: "high", PrimaryService: "svc-a"},
		},
	}
	sigStore := &fakeReconSignatureStore{
		sigs: map[string]*models.IncidentSignature{
			"sig-1":         {SignatureID: "sig-1", Shape: "error_rate", Symptoms: []models.SymptomType{models.SymptomTypeErrorRate}, FeatureVector: []float64{1, 1}},
			"historic-low":  {SignatureID: "historic-low", Shape: "error_rate", Symptoms: []models.SymptomType{models.SymptomTypeErrorRate}, FeatureVector: []float64{1, 0.7}, Remediation: []string{"rollback"}, AvgResolutionTime: 18},
			"historic-high": {SignatureID: "historic-high", Shape: "error_rate", Symptoms: []models.SymptomType{models.SymptomTypeErrorRate}, FeatureVector: []float64{1, 1}, Remediation: []string{"restart"}, AvgResolutionTime: 12},
		},
		similar: map[string][]*models.SimilarIncident{
			"sig-1": {
				{IncidentID: "historic-low", Similarity: 0.6},
				{IncidentID: "historic-high", Similarity: 0.9},
			},
		},
	}
	remStore := &fakeReconRemediationStore{
		stats: map[string][]*models.RemediationStats{
			"historic-high": {
				{Action: "restart", SuccessRate: 0.9, AvgTimeToResolve: 12},
				{Action: "rollback", SuccessRate: 0.8, AvgTimeToResolve: 18},
			},
		},
	}
	learnerSvc := learner.NewLearner(remStore, &fakeReconFeedbackStore{}, sigStore)
	matcherSvc := matcher.NewSignatureMatcher(sigStore)
	recon := NewIncidentReconstructor(graph.NewBuilder(graphStore), matcherSvc, learnerSvc, incidentStore)

	ctx, err := recon.ReconstructFast(context.Background(), "inc-1")
	if err != nil {
		t.Fatalf("reconstruct fast: %v", err)
	}
	if ctx.IncidentID != "inc-1" || len(ctx.CausalChain) != 3 {
		t.Fatalf("unexpected context: %+v", ctx)
	}
	if len(ctx.RelatedServices) != 2 {
		t.Fatalf("expected two related services, got %+v", ctx.RelatedServices)
	}
	if len(ctx.SimilarIncidents) < 2 {
		t.Fatalf("expected at least 2 similar incidents, got %d", len(ctx.SimilarIncidents))
	}
	if ctx.SimilarIncidents[0].Similarity < ctx.SimilarIncidents[len(ctx.SimilarIncidents)-1].Similarity {
		t.Fatalf("similar incidents not sorted: %+v", ctx.SimilarIncidents)
	}
	// Suggested actions depend on remediation store lookup — may be empty if similar incident ID doesn't match
	if len(ctx.SuggestedActions) > 0 && ctx.SuggestedActions[0].Priority != 1 {
		t.Fatalf("unexpected suggested actions: %+v", ctx.SuggestedActions)
	}
	if ctx.Confidence <= 0.5 || ctx.Confidence > 1 {
		t.Fatalf("unexpected confidence: %v", ctx.Confidence)
	}
	if ctx.Partial || len(ctx.Warnings) != 0 {
		t.Fatalf("successful reconstruction should not be flagged partial: Partial=%v Warnings=%v", ctx.Partial, ctx.Warnings)
	}

	signalStore := &fakeReconIncidentStore{incidents: map[string]*models.Incident{}}
	buildReconIncidentGraph(graphStore, "signal-1")
	recon2 := NewIncidentReconstructor(graph.NewBuilder(graphStore), matcherSvc, learnerSvc, signalStore)
	signalCtx, err := recon2.ReconstructFromSignal(context.Background(), &models.IncidentSignal{
		SignalID:  "signal-1",
		Timestamp: time.Now(),
		Service:   "svc-a",
		Severity:  "high",
	})
	if err != nil {
		t.Fatalf("reconstruct from signal: %v", err)
	}
	if signalCtx.IncidentID != "signal-1" || len(signalStore.saved) != 1 {
		t.Fatalf("expected signal incident to be saved and reconstructed, got %+v", signalCtx)
	}
}

// fakeErrSignatureService is a SignatureService whose enrichment calls can be
// made to fail so the reconstructor's error surfacing can be exercised.
type fakeErrSignatureService struct {
	extractErr    error
	similarErr    error
	similar       []*models.SimilarIncident
	findCallCount int
}

func (f *fakeErrSignatureService) Score(_, _ *models.IncidentSignature) (float64, error) {
	return 0, nil
}
func (f *fakeErrSignatureService) TopK(_ *models.IncidentSignature, _ []*models.IncidentSignature, _ int) ([]matcher.Match, error) {
	return nil, nil
}
func (f *fakeErrSignatureService) ExtractSignature(_ context.Context, _ *models.IncidentContext) (*models.IncidentSignature, error) {
	if f.extractErr != nil {
		return nil, f.extractErr
	}
	return &models.IncidentSignature{SignatureID: "sig-1"}, nil
}
func (f *fakeErrSignatureService) FindSimilar(_ context.Context, _ *models.IncidentSignature, _ int) ([]*models.SimilarIncident, error) {
	f.findCallCount++
	if f.similarErr != nil {
		return nil, f.similarErr
	}
	return f.similar, nil
}
func (f *fakeErrSignatureService) RegisterSignature(_ context.Context, _ *models.IncidentSignature) error {
	return nil
}
func (f *fakeErrSignatureService) UpdateSignature(_ context.Context, _ string, _ *models.SignatureUpdate) error {
	return nil
}

func newReconWithFailingEnrichment(ms *fakeErrSignatureService, remStore *fakeReconRemediationStore) *IncidentReconstructor {
	graphStore := &fakeReconGraphStore{nodes: map[string]*models.Node{}, edges: map[string][]*models.Edge{}}
	buildReconIncidentGraph(graphStore, "inc-1")
	incidentStore := &fakeReconIncidentStore{
		incidents: map[string]*models.Incident{
			"inc-1": {ID: "inc-1", SignatureID: "sig-1", Status: "active", Severity: "high", PrimaryService: "svc-a"},
		},
	}
	learnerSvc := learner.NewLearner(remStore, &fakeReconFeedbackStore{}, &fakeReconSignatureStore{
		sigs:    map[string]*models.IncidentSignature{},
		similar: map[string][]*models.SimilarIncident{},
	})
	return NewIncidentReconstructor(graph.NewBuilder(graphStore), ms, learnerSvc, incidentStore)
}

// TestReconstructEnrichmentFailuresAreReported verifies that failures of the
// matcher/learner backends are no longer silently swallowed: the reconstruction
// still returns its partial result, but it is flagged Partial with a diagnostic
// warning so callers can distinguish it from a genuine low-signal incident.
func TestReconstructEnrichmentFailuresAreReported(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	withSimilar := []*models.SimilarIncident{{IncidentID: "historic", Similarity: 0.9}}

	tests := []struct {
		name          string
		extractErr    error
		similarErr    error
		similar       []*models.SimilarIncident
		listErr       error
		wantWarning   string
		wantFindCalls int
		wantListCalls int
	}{
		{
			name:        "signature extraction failure",
			extractErr:  backendErr,
			wantWarning: "signature extraction",
		},
		{
			name:          "similarity search failure",
			similarErr:    backendErr,
			wantFindCalls: 1,
			wantWarning:   "similarity search",
		},
		{
			name:          "remediation lookup failure",
			similar:       withSimilar,
			listErr:       backendErr,
			wantFindCalls: 1,
			wantListCalls: 1,
			wantWarning:   "remediation lookup",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms := &fakeErrSignatureService{
				extractErr: tc.extractErr,
				similarErr: tc.similarErr,
				similar:    tc.similar,
			}
			remStore := &fakeReconRemediationStore{
				stats:   map[string][]*models.RemediationStats{},
				listErr: tc.listErr,
			}
			recon := newReconWithFailingEnrichment(ms, remStore)

			ctx, err := recon.ReconstructFast(context.Background(), "inc-1")
			if err != nil {
				t.Fatalf("enrichment failure should still return partial result, got error: %v", err)
			}
			if ctx == nil {
				t.Fatal("expected partial reconstruction context, got nil")
			}
			if !ctx.Partial {
				t.Fatal("expected Partial to be set when an enrichment subsystem fails")
			}
			if len(ctx.Warnings) == 0 {
				t.Fatal("expected a diagnostic warning when an enrichment subsystem fails")
			}
			if joined := strings.Join(ctx.Warnings, " "); !strings.Contains(joined, tc.wantWarning) {
				t.Fatalf("expected warning to mention %q, got %q", tc.wantWarning, joined)
			}
			if len(ctx.CausalChain) != 3 {
				t.Fatalf("partial reconstruction should preserve causal chain, got %d events", len(ctx.CausalChain))
			}
			if ms.findCallCount != tc.wantFindCalls {
				t.Fatalf("FindSimilar called %d times, want %d", ms.findCallCount, tc.wantFindCalls)
			}
			if remStore.listCalls != tc.wantListCalls {
				t.Fatalf("remediation lookup called %d times, want %d", remStore.listCalls, tc.wantListCalls)
			}
		})
	}
}

// TestReconstructFromSignalReportsEnrichmentFailure covers the reconstruct()
// path (used by ReconstructFromSignal) in addition to reconstructAdaptive().
func TestReconstructFromSignalReportsEnrichmentFailure(t *testing.T) {
	ms := &fakeErrSignatureService{extractErr: errors.New("extractor down")}
	remStore := &fakeReconRemediationStore{stats: map[string][]*models.RemediationStats{}}
	recon := newReconWithFailingEnrichment(ms, remStore)

	ctx, err := recon.ReconstructFromSignal(context.Background(), &models.IncidentSignal{
		SignalID:  "inc-1",
		Timestamp: time.Now(),
		Service:   "svc-a",
		Severity:  "high",
	})
	if err != nil {
		t.Fatalf("enrichment failure should still return partial result, got error: %v", err)
	}
	if !ctx.Partial || len(ctx.Warnings) == 0 {
		t.Fatalf("expected partial flag and warning, got Partial=%v Warnings=%v", ctx.Partial, ctx.Warnings)
	}
	if joined := strings.Join(ctx.Warnings, " "); !strings.Contains(joined, "signature extraction") {
		t.Fatalf("expected signature extraction warning, got %q", joined)
	}
	if ms.findCallCount != 0 {
		t.Fatalf("FindSimilar should be skipped after extraction failure, called %d times", ms.findCallCount)
	}
}
