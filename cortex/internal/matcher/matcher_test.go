package matcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
)

type fakeSignatureStore struct {
	sigs      map[string]*models.IncidentSignature
	saved     []*models.IncidentSignature
	similar   map[string][]*models.SimilarIncident
	listCalls int
	listErr   error
}

func (f *fakeSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	f.saved = append(f.saved, sig)
	f.sigs[sig.SignatureID] = sig
	return nil
}

func (f *fakeSignatureStore) Get(_ context.Context, id string) (*models.IncidentSignature, error) {
	if sig, ok := f.sigs[id]; ok {
		return sig, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeSignatureStore) List(_ context.Context, limit int) ([]*models.IncidentSignature, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*models.IncidentSignature
	for _, sig := range f.sigs {
		out = append(out, sig)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeSignatureStore) FindSimilar(_ context.Context, sig *models.IncidentSignature, limit int) ([]*models.SimilarIncident, error) {
	items := f.similar[sig.SignatureID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (f *fakeSignatureStore) FindByBehavioralHash(_ context.Context, hash string) ([]*models.IncidentSignature, error) {
	var out []*models.IncidentSignature
	for _, sig := range f.sigs {
		if sig.BehavioralHash == hash {
			out = append(out, sig)
		}
	}
	return out, nil
}

func (f *fakeSignatureStore) UpdateDecay(_ context.Context, id string, factor float64) error {
	if sig, ok := f.sigs[id]; ok {
		sig.DecayFactor = factor
	}
	return nil
}

func (f *fakeSignatureStore) ArchiveStale(_ context.Context, threshold float64) (int, error) {
	return 0, nil
}

func (f *fakeSignatureStore) UpdateLastMatched(_ context.Context, id string) error {
	return nil
}

// TestServiceRoleDeterministicForOverlappingNames locks in a stable
// classification for names whose patterns match more than one role. Before
// serviceRole used an ordered slice, it ranged over a map, so Go's randomized
// iteration order made these results vary between calls.
func TestServiceRoleDeterministicForOverlappingNames(t *testing.T) {
	cases := map[string]string{
		"redis-cache":     "database", // "redis" (database) precedes "cache" (cache)
		"event-processor": "queue",    // "event" (queue) precedes "processor" (worker)
		"app-worker":      "api",      // "app" (api) precedes "worker" (worker)
		"user-auth":       "auth",     // "auth" (auth) precedes "user" (user)
		"memcached":       "cache",
		"postgres-db":     "database",
		"kafka-broker":    "queue",
		"orders-api":      "api",
		"mystery-svc":     "service",
	}

	for name, want := range cases {
		for i := 0; i < 100; i++ {
			if got := serviceRole(name); got != want {
				t.Fatalf("serviceRole(%q) = %q on iteration %d, want %q", name, got, i, want)
			}
		}
	}
}

func TestSimilarityHelpers(t *testing.T) {
	if got := ComputeCosineSimilarity([]float64{1, 2}, []float64{1, 2}); got < 0.999 || got > 1.001 {
		t.Fatalf("expected cosine similarity close to 1, got %v", got)
	}
	if got := ComputeJaccardSimilarity([]string{"a", "b"}, []string{"a", "b"}); got != 1 {
		t.Fatalf("expected jaccard similarity 1, got %v", got)
	}
}

func TestSimilarityHelpersMatchExportedImplementations(t *testing.T) {
	cosineCases := []struct {
		name string
		a, b []float64
	}{
		{"parallel", []float64{1, 2, 3}, []float64{1, 2, 3}},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}},
		{"opposing", []float64{1, 0}, []float64{-1, 0}},
		{"empty", nil, []float64{1}},
	}
	for _, tc := range cosineCases {
		t.Run(tc.name, func(t *testing.T) {
			got := cosineSimilarity(tc.a, tc.b)
			want := ComputeCosineSimilarity(tc.a, tc.b)
			if got != want {
				t.Fatalf("cosineSimilarity(%v, %v) = %v, ComputeCosineSimilarity = %v", tc.a, tc.b, got, want)
			}
			if got < 0 || got > 1 {
				t.Fatalf("cosine similarity %v is outside [0, 1]", got)
			}
		})
	}

	jaccardCases := []struct {
		a, b []string
	}{
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"b", "c"}},
		{nil, nil},
		{[]string{"a"}, nil},
	}
	for _, tc := range jaccardCases {
		if got, want := jaccardSimilarity(tc.a, tc.b), ComputeJaccardSimilarity(tc.a, tc.b); got != want {
			t.Fatalf("jaccardSimilarity(%v, %v) = %v, ComputeJaccardSimilarity = %v", tc.a, tc.b, got, want)
		}
	}
}

func TestTopKAndScore(t *testing.T) {
	store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{}}
	m := NewSignatureMatcher(store)

	query := &models.IncidentSignature{
		SignatureID:   "q",
		FeatureVector: []float64{1, 0},
		Symptoms:      []models.SymptomType{models.SymptomTypeErrorRate},
		ServiceRoles:  []string{"svc-a"},
	}
	candidates := []*models.IncidentSignature{
		{
			SignatureID:   "low",
			FeatureVector: []float64{0, 1},
			Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
			ServiceRoles:  []string{"svc-b"},
		},
		{
			SignatureID:   "high",
			FeatureVector: []float64{1, 0},
			Symptoms:      []models.SymptomType{models.SymptomTypeErrorRate},
			ServiceRoles:  []string{"svc-a"},
		},
	}

	score, err := m.Score(query, candidates[1])
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if score < 0.89 || score > 0.91 {
		t.Fatalf("unexpected score: %v", score)
	}

	matches, err := m.TopK(query, candidates, 1)
	if err != nil {
		t.Fatalf("topk: %v", err)
	}
	if len(matches) != 1 || matches[0].Signature.SignatureID != "high" {
		t.Fatalf("unexpected top match: %+v", matches)
	}
}

func TestExtractAndRegisterSignature(t *testing.T) {
	store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{}}
	m := NewSignatureMatcher(store)
	incident := &models.IncidentContext{
		IncidentID: "inc-1",
		CausalChain: []*models.CausalEvent{
			{Kind: "log", Timestamp: time.Now()},
			{Kind: "metric", Timestamp: time.Now().Add(30 * time.Second)},
		},
		RelatedServices: []string{"svc-a", "svc-b"},
		Symptoms: []models.Symptom{
			{Type: models.SymptomTypeErrorRate, Description: "error spike"},
		},
		Confidence: 0.5,
	}

	sig, err := m.ExtractSignature(context.Background(), incident)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if sig.Shape == "" || sig.TemporalPattern.PropagationSpeed != "fast" {
		t.Fatalf("unexpected signature: %+v", sig)
	}
	if sig.FeatureVector[0] < 1 || sig.FeatureVector[1] != 1 {
		t.Fatalf("unexpected feature vector: %+v", sig.FeatureVector)
	}

	if err := m.RegisterSignature(context.Background(), sig); err != nil {
		t.Fatalf("register: %v", err)
	}
	if sig.OccurrenceCount != 1 {
		t.Fatalf("expected occurrence count 1, got %d", sig.OccurrenceCount)
	}

	store.sigs[sig.SignatureID] = &models.IncidentSignature{
		SignatureID:       sig.SignatureID,
		OccurrenceCount:   1,
		AvgResolutionTime: 10,
	}
	if err := m.UpdateSignature(context.Background(), sig.SignatureID, &models.SignatureUpdate{
		UpdateResolutionTime: 20,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated := store.sigs[sig.SignatureID]
	if updated.OccurrenceCount != 2 || updated.AvgResolutionTime != 15 {
		t.Fatalf("unexpected update result: %+v", updated)
	}
}

func TestUpdateSignatureOccurrenceIncrementOnce(t *testing.T) {
	tests := []struct {
		name           string
		count          int
		avg            int64
		increment      bool
		resolutionTime int64
		wantCount      int
		wantAvg        int64
	}{
		{
			name:           "increment and resolution time are one event",
			count:          1,
			avg:            10,
			increment:      true,
			resolutionTime: 20,
			wantCount:      2,
			wantAvg:        15,
		},
		{
			name:           "resolution time only",
			count:          1,
			avg:            10,
			resolutionTime: 20,
			wantCount:      2,
			wantAvg:        15,
		},
		{
			name:      "increment only",
			count:     1,
			avg:       10,
			increment: true,
			wantCount: 2,
			wantAvg:   10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{
				"sig": {SignatureID: "sig", OccurrenceCount: tt.count, AvgResolutionTime: tt.avg},
			}}
			m := NewSignatureMatcher(store)
			err := m.UpdateSignature(context.Background(), "sig", &models.SignatureUpdate{
				IncrementOccurrence:  tt.increment,
				UpdateResolutionTime: tt.resolutionTime,
			})
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			got := store.sigs["sig"]
			if got.OccurrenceCount != tt.wantCount || got.AvgResolutionTime != tt.wantAvg {
				t.Fatalf("got count=%d avg=%d, want count=%d avg=%d",
					got.OccurrenceCount, got.AvgResolutionTime, tt.wantCount, tt.wantAvg)
			}
		})
	}
}

// stampedeStore counts List calls concurrently and can hold the first one
// open so a window for concurrent cache misses can be created deterministically.
type stampedeStore struct {
	fakeSignatureStore

	mu        sync.Mutex
	listCalls int
	block     bool
	entered   chan struct{}
	release   chan struct{}
	items     []*models.IncidentSignature
}

func (s *stampedeStore) List(_ context.Context, limit int) ([]*models.IncidentSignature, error) {
	s.mu.Lock()
	s.listCalls++
	block := s.block
	s.mu.Unlock()

	if block {
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-s.release
	}

	out := append([]*models.IncidentSignature(nil), s.items...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *stampedeStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls
}

func TestNewConfiguredSignatureMatcher(t *testing.T) {
	store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{}}

	svc, err := NewConfiguredSignatureMatcher(store, config.MatcherConfig{Mode: "go"})
	if err != nil {
		t.Fatalf("go matcher should be available: %v", err)
	}
	if _, ok := svc.(*SignatureMatcher); !ok {
		t.Fatalf("expected Go-backed signature matcher, got %T", svc)
	}

	if _, err := NewConfiguredSignatureMatcher(store, config.MatcherConfig{Mode: "rust"}); !errors.Is(err, ErrRustNotAvailable) {
		t.Fatalf("expected rust matcher to be optional and unavailable, got %v", err)
	}
}

func TestFindSimilarUsesConfiguredThreshold(t *testing.T) {
	query := &models.IncidentSignature{
		SignatureID:   "query",
		Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
		FeatureVector: []float64{1, 0},
	}
	high := &models.IncidentSignature{
		SignatureID:   "high",
		Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
		FeatureVector: []float64{1, 0},
	}
	low := &models.IncidentSignature{SignatureID: "low"}

	for _, tc := range []struct {
		name       string
		threshold  float64
		wantIDs    []string
		wantScores []float64
	}{
		{
			name:       "zero uses default 0.5 threshold",
			threshold:  0,
			wantIDs:    []string{"high"},
			wantScores: []float64{0.9},
		},
		{
			name:       "configured threshold below default",
			threshold:  0.05,
			wantIDs:    []string{"high", "low"},
			wantScores: []float64{0.9, 0.1},
		},
		{
			name:       "threshold 0.5",
			threshold:  0.5,
			wantIDs:    []string{"high"},
			wantScores: []float64{0.9},
		},
		{
			name:      "threshold 0.95",
			threshold: 0.95,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{
				query.SignatureID: query,
				high.SignatureID:  high,
				low.SignatureID:   low,
			}}
			svc := NewSignatureMatcherWithEngine(store, NewGoMatcher(1), config.MatcherConfig{
				CacheSize:              10,
				MinSimilarityThreshold: tc.threshold,
			})
			matches, err := svc.FindSimilar(context.Background(), query, 5)
			if err != nil {
				t.Fatalf("find similar: %v", err)
			}
			if len(matches) != len(tc.wantIDs) {
				t.Fatalf("expected %d matches, got %d: %v", len(tc.wantIDs), len(matches), matches)
			}
			for i, wantID := range tc.wantIDs {
				if matches[i].IncidentID != wantID {
					t.Errorf("match %d ID = %q, want %q", i, matches[i].IncidentID, wantID)
				}
				if matches[i].Similarity != tc.wantScores[i] {
					t.Errorf("match %q similarity = %v, want %v", wantID, matches[i].Similarity, tc.wantScores[i])
				}
			}
		})
	}
}

func TestFindSimilarFallbackUsesConfiguredThreshold(t *testing.T) {
	query := &models.IncidentSignature{SignatureID: "query"}
	store := &fakeSignatureStore{
		sigs:    map[string]*models.IncidentSignature{},
		listErr: errors.New("list failed"),
		similar: map[string][]*models.SimilarIncident{
			"query": {
				{IncidentID: "high", Similarity: 0.9},
				{IncidentID: "medium", Similarity: 0.8},
				{IncidentID: "low", Similarity: 0.4},
			},
		},
	}
	for _, tc := range []struct {
		name      string
		threshold float64
		wantIDs   []string
	}{
		{name: "default threshold", threshold: 0, wantIDs: []string{"high", "medium"}},
		{name: "high threshold retains qualifying result", threshold: 0.85, wantIDs: []string{"high"}},
		{name: "below storage default admits low score", threshold: 0.3, wantIDs: []string{"high", "medium", "low"}},
		{name: "above all results", threshold: 0.95},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewSignatureMatcherWithEngine(store, NewGoMatcher(1), config.MatcherConfig{
				MinSimilarityThreshold: tc.threshold,
			})
			matches, err := svc.FindSimilar(context.Background(), query, 5)
			if err != nil {
				t.Fatalf("find similar: %v", err)
			}
			if len(matches) != len(tc.wantIDs) {
				t.Fatalf("expected %d matches, got %d: %v", len(tc.wantIDs), len(matches), matches)
			}
			for i, wantID := range tc.wantIDs {
				if matches[i].IncidentID != wantID {
					t.Errorf("match %d ID = %q, want %q", i, matches[i].IncidentID, wantID)
				}
			}
		})
	}
}

func TestFindSimilarUsesCacheAndInvalidatesOnWrite(t *testing.T) {
	store := &fakeSignatureStore{sigs: map[string]*models.IncidentSignature{
		"q": {SignatureID: "q", Shape: "deploy->timeout", Symptoms: []models.SymptomType{models.SymptomTypeTimeout}, FeatureVector: []float64{1, 1}},
		"a": {SignatureID: "a", Shape: "deploy->timeout", Symptoms: []models.SymptomType{models.SymptomTypeTimeout}, FeatureVector: []float64{1, 1}},
		"b": {SignatureID: "b", Shape: "deploy->error", Symptoms: []models.SymptomType{models.SymptomTypeErrorRate}, FeatureVector: []float64{1, 0}},
	}}

	svc, err := NewConfiguredSignatureMatcher(store, config.MatcherConfig{
		Mode:        "go",
		Parallelism: 2,
		CacheSize:   100,
		CacheTTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("new matcher: %v", err)
	}

	query := store.sigs["q"]
	first, err := svc.FindSimilar(context.Background(), query, 2)
	if err != nil {
		t.Fatalf("first find similar: %v", err)
	}
	second, err := svc.FindSimilar(context.Background(), query, 2)
	if err != nil {
		t.Fatalf("second find similar: %v", err)
	}
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("expected similar matches, got %v and %v", first, second)
	}
	if store.listCalls != 1 {
		t.Fatalf("expected cached candidate list, got %d store list calls", store.listCalls)
	}

	if err := svc.RegisterSignature(context.Background(), &models.IncidentSignature{
		SignatureID:   "c",
		Shape:         "deploy->timeout",
		Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
		FeatureVector: []float64{1, 1},
	}); err != nil {
		t.Fatalf("register signature: %v", err)
	}

	if _, err := svc.FindSimilar(context.Background(), query, 2); err != nil {
		t.Fatalf("find similar after invalidate: %v", err)
	}
	if store.listCalls != 2 {
		t.Fatalf("expected cache invalidation after write, got %d store list calls", store.listCalls)
	}
}

func TestLoadCandidatesSingleFlight(t *testing.T) {
	const workers = 8

	store := &stampedeStore{
		block:   true,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		items: []*models.IncidentSignature{
			{SignatureID: "a", FeatureVector: []float64{1, 1}},
		},
	}
	m := NewSignatureMatcherWithEngine(store, NewGoMatcher(1), config.MatcherConfig{
		CacheSize: 10,
		CacheTTL:  time.Minute,
	})

	ctx := context.Background()
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			if _, err := m.loadCandidates(ctx); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	close(start)

	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("expected a candidate List call")
	}

	// Keep the first List in flight until every other worker has reached the
	// refresh path. With the single-flight lock they wait for it and reuse the
	// repopulated cache; without it they all issue their own List.
	time.Sleep(100 * time.Millisecond)
	close(store.release)

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("loadCandidates: %v", err)
	}

	if got := store.calls(); got != 1 {
		t.Fatalf("expected exactly one List call for concurrent cache misses, got %d", got)
	}
}
