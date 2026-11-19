package memory

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// fakeSignatureStore is an in-memory SignatureStore that mimics the upsert
// semantics of the real stores: Save replaces a record with the same ID and
// inserts otherwise, preserving insertion order for List.
type fakeSignatureStore struct {
	sigs  map[string]*models.IncidentSignature
	order []string
}

var _ storage.SignatureStore = (*fakeSignatureStore)(nil)

func newFakeSignatureStore(sigs ...*models.IncidentSignature) *fakeSignatureStore {
	f := &fakeSignatureStore{sigs: make(map[string]*models.IncidentSignature)}
	for _, sig := range sigs {
		f.Save(context.Background(), sig)
	}
	return f
}

func (f *fakeSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	if _, ok := f.sigs[sig.SignatureID]; !ok {
		f.order = append(f.order, sig.SignatureID)
	}
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
	result := make([]*models.IncidentSignature, 0, len(f.order))
	for _, id := range f.order {
		result = append(result, f.sigs[id])
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (*fakeSignatureStore) FindSimilar(context.Context, *models.IncidentSignature, int) ([]*models.SimilarIncident, error) {
	return nil, nil
}

// FindByBehavioralHash always misses so Evolve takes the temporal-merge path.
func (*fakeSignatureStore) FindByBehavioralHash(context.Context, string) ([]*models.IncidentSignature, error) {
	return nil, nil
}

func (*fakeSignatureStore) UpdateDecay(context.Context, string, float64) error { return nil }
func (*fakeSignatureStore) ArchiveStale(context.Context, float64) (int, error) { return 0, nil }
func (*fakeSignatureStore) UpdateLastMatched(context.Context, string) error    { return nil }

func temporalCandidate() *models.IncidentSignature {
	return &models.IncidentSignature{
		SignatureID:       "parent-sig",
		Shape:             "deploy->timeout",
		TemporalPattern:   models.TemporalPattern{TriggerToSymptom: 0},
		FeatureVector:     []float64{1, 0},
		OccurrenceCount:   5,
		Version:           3,
		ParentSignatureID: "",
	}
}

func temporalIncoming(id string) *models.IncidentSignature {
	return &models.IncidentSignature{
		SignatureID:     id,
		Shape:           "deploy->timeout",
		TemporalPattern: models.TemporalPattern{TriggerToSymptom: 0},
		FeatureVector:   []float64{0, 1},
	}
}

func TestEvolveTemporalMergeCreatesDistinctChildSignature(t *testing.T) {
	parent := temporalCandidate()
	store := newFakeSignatureStore(parent)
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	merged, err := ev.Evolve(context.Background(), temporalIncoming("incident-1"))
	if err != nil {
		t.Fatalf("Evolve returned error: %v", err)
	}

	if merged.SignatureID == "" {
		t.Fatal("merged signature has an empty SignatureID")
	}
	if merged.SignatureID == parent.SignatureID {
		t.Fatalf("merged signature reused parent ID %q; expected a distinct child ID", parent.SignatureID)
	}
	if merged.ParentSignatureID != parent.SignatureID {
		t.Fatalf("ParentSignatureID = %q, want %q", merged.ParentSignatureID, parent.SignatureID)
	}
	if merged.Version != parent.Version+1 {
		t.Fatalf("Version = %d, want %d", merged.Version, parent.Version+1)
	}

	// The parent must not have been overwritten by the child.
	storedParent, err := store.Get(context.Background(), parent.SignatureID)
	if err != nil {
		t.Fatalf("parent signature was removed: %v", err)
	}
	if storedParent.OccurrenceCount != parent.OccurrenceCount || storedParent.Version != parent.Version {
		t.Fatalf("parent signature was overwritten: got occurrence=%d version=%d, want occurrence=%d version=%d",
			storedParent.OccurrenceCount, storedParent.Version, parent.OccurrenceCount, parent.Version)
	}

	// The child is persisted under its own ID.
	storedChild, err := store.Get(context.Background(), merged.SignatureID)
	if err != nil {
		t.Fatalf("child signature was not saved: %v", err)
	}
	if storedChild.ParentSignatureID != parent.SignatureID {
		t.Fatalf("persisted child ParentSignatureID = %q, want %q", storedChild.ParentSignatureID, parent.SignatureID)
	}
}

func TestEvolveTemporalMergeDoesNotCollapseIncidents(t *testing.T) {
	store := newFakeSignatureStore(temporalCandidate())
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	first, err := ev.Evolve(context.Background(), temporalIncoming("incident-1"))
	if err != nil {
		t.Fatalf("first Evolve returned error: %v", err)
	}
	second, err := ev.Evolve(context.Background(), temporalIncoming("incident-2"))
	if err != nil {
		t.Fatalf("second Evolve returned error: %v", err)
	}

	if first.SignatureID == second.SignatureID {
		t.Fatalf("two temporal merges collapsed into one record with ID %q", first.SignatureID)
	}

	sigs, err := store.List(context.Background(), 1000)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sigs) != 3 {
		t.Fatalf("stored signatures = %d, want 3 (parent + two distinct children)", len(sigs))
	}
}

// stubSignatureStore is a minimal in-memory SignatureStore for Decay tests.
type stubSignatureStore struct {
	sigs         []*models.IncidentSignature
	decayUpdates map[string]float64
}

func (s *stubSignatureStore) Save(context.Context, *models.IncidentSignature) error { return nil }

func (s *stubSignatureStore) Get(context.Context, string) (*models.IncidentSignature, error) {
	return nil, nil
}

func (s *stubSignatureStore) List(context.Context, int) ([]*models.IncidentSignature, error) {
	return s.sigs, nil
}

func (s *stubSignatureStore) FindSimilar(context.Context, *models.IncidentSignature, int) ([]*models.SimilarIncident, error) {
	return nil, nil
}

func (s *stubSignatureStore) FindByBehavioralHash(context.Context, string) ([]*models.IncidentSignature, error) {
	return nil, nil
}

func (s *stubSignatureStore) UpdateDecay(_ context.Context, signatureID string, factor float64) error {
	if s.decayUpdates == nil {
		s.decayUpdates = make(map[string]float64)
	}
	s.decayUpdates[signatureID] = factor
	return nil
}

func (s *stubSignatureStore) ArchiveStale(context.Context, float64) (int, error) { return 0, nil }

func (s *stubSignatureStore) UpdateLastMatched(context.Context, string) error { return nil }

var _ storage.SignatureStore = (*stubSignatureStore)(nil)

func decayTestSignature(id string, matchedAt time.Time) *models.IncidentSignature {
	return &models.IncidentSignature{
		SignatureID:   id,
		DecayFactor:   1.0,
		LastMatchedAt: &matchedAt,
	}
}

// TestDecayNonPositivePeriodDoesNotPanic reproduces the reported integer
// divide-by-zero: a zero (or negative) DecayPeriod must never reach the
// elapsed/DecayPeriod division in Decay.
func TestDecayNonPositivePeriodDoesNotPanic(t *testing.T) {
	for _, period := range []time.Duration{0, -time.Hour} {
		t.Run(period.String(), func(t *testing.T) {
			past := time.Now().Add(-time.Hour)
			store := &stubSignatureStore{
				sigs: []*models.IncidentSignature{decayTestSignature("sig-1", past)},
			}
			ev := NewSignatureEvolver(Config{
				DecayPeriod:      period,
				DecayRate:        0.1,
				ArchiveThreshold: 0.1,
			}, store, nil)

			count, err := ev.Decay(context.Background())
			if err != nil {
				t.Fatalf("Decay returned error with period %v: %v", period, err)
			}
			if count != 0 {
				t.Fatalf("expected 0 affected signatures with period %v, got %d", period, count)
			}
			if len(store.decayUpdates) != 0 {
				t.Fatalf("expected no decay updates with period %v, got %v", period, store.decayUpdates)
			}
		})
	}
}

// TestDecayValidPeriodStillApplies guards against over-correcting the fix:
// with a positive period, decay must still be applied as before.
func TestDecayValidPeriodStillApplies(t *testing.T) {
	past := time.Now().Add(-3*time.Hour - time.Minute)
	store := &stubSignatureStore{
		sigs: []*models.IncidentSignature{decayTestSignature("sig-1", past)},
	}
	ev := NewSignatureEvolver(Config{
		DecayPeriod:      time.Hour,
		DecayRate:        0.5,
		ArchiveThreshold: 0.1,
	}, store, nil)

	count, err := ev.Decay(context.Background())
	if err != nil {
		t.Fatalf("Decay returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 affected signature, got %d", count)
	}
	factor, ok := store.decayUpdates["sig-1"]
	if !ok {
		t.Fatal("expected a decay update for sig-1")
	}
	// Three elapsed periods at rate 0.5: 1.0 * (1-0.5)^3 = 0.125.
	if math.Abs(factor-0.125) > 1e-9 {
		t.Fatalf("expected decay factor 0.125, got %v", factor)
	}
}

// TestFromConfigNormalizesNonPositiveDecayPeriod verifies that config built
// through FromConfig never carries a non-positive DecayPeriod.
func TestFromConfigNormalizesNonPositiveDecayPeriod(t *testing.T) {
	for _, period := range []time.Duration{0, -time.Hour} {
		cfg := FromConfig(period, 0.1, 0.1, 0.3, 0.1)
		if cfg.DecayPeriod <= 0 {
			t.Fatalf("FromConfig(%v) produced non-positive DecayPeriod %v", period, cfg.DecayPeriod)
		}
	}
}

// configurableSignatureStore is a SignatureStore fake that can inject errors
// from the read paths used by Evolve and record calls to Save.
type configurableSignatureStore struct {
	saved             []*models.IncidentSignature
	findByHashResults []*models.IncidentSignature
	findByHashErr     error
	listResults       []*models.IncidentSignature
	listErr           error
}

func (f *configurableSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	f.saved = append(f.saved, sig)
	return nil
}

func (*configurableSignatureStore) Get(context.Context, string) (*models.IncidentSignature, error) {
	return nil, nil
}

func (f *configurableSignatureStore) List(context.Context, int) ([]*models.IncidentSignature, error) {
	return f.listResults, f.listErr
}

func (*configurableSignatureStore) FindSimilar(context.Context, *models.IncidentSignature, int) ([]*models.SimilarIncident, error) {
	return nil, nil
}

func (f *configurableSignatureStore) FindByBehavioralHash(context.Context, string) ([]*models.IncidentSignature, error) {
	return f.findByHashResults, f.findByHashErr
}

func (*configurableSignatureStore) UpdateDecay(context.Context, string, float64) error {
	return nil
}

func (*configurableSignatureStore) ArchiveStale(context.Context, float64) (int, error) {
	return 0, nil
}

func (*configurableSignatureStore) UpdateLastMatched(context.Context, string) error {
	return nil
}

func TestEvolveReturnsBehavioralHashStoreError(t *testing.T) {
	storeErr := errors.New("behavioral hash query failed")
	store := &configurableSignatureStore{findByHashErr: storeErr}
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	got, err := ev.Evolve(context.Background(), &models.IncidentSignature{SignatureID: "sig-1"})
	if !errors.Is(err, storeErr) {
		t.Fatalf("expected wrapped behavioral-hash store error, got %v", err)
	}
	if !strings.Contains(err.Error(), "behavioral hash") {
		t.Fatalf("expected error to carry context, got %q", err.Error())
	}
	if got != nil {
		t.Fatalf("expected nil signature on store error, got %+v", got)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected no Save calls on store error, got %d", len(store.saved))
	}
}

func TestEvolveReturnsListStoreError(t *testing.T) {
	storeErr := errors.New("list query failed")
	store := &configurableSignatureStore{listErr: storeErr}
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	got, err := ev.Evolve(context.Background(), &models.IncidentSignature{SignatureID: "sig-1"})
	if !errors.Is(err, storeErr) {
		t.Fatalf("expected wrapped list store error, got %v", err)
	}
	if !strings.Contains(err.Error(), "list signatures") {
		t.Fatalf("expected error to carry context, got %q", err.Error())
	}
	if got != nil {
		t.Fatalf("expected nil signature on store error, got %+v", got)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected no Save calls on store error, got %d", len(store.saved))
	}
}

func TestEvolveCreatesNewSignatureWhenNoMatches(t *testing.T) {
	store := &configurableSignatureStore{}
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	sig := &models.IncidentSignature{SignatureID: "sig-new", Shape: "cascade"}
	got, err := ev.Evolve(context.Background(), sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.SignatureID != "sig-new" {
		t.Fatalf("expected new signature to be returned, got %+v", got)
	}
	if got.Version != 1 || got.DecayFactor != 1.0 {
		t.Fatalf("expected initialized version/decay factor, got version=%d decay=%v", got.Version, got.DecayFactor)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected exactly one Save call, got %d", len(store.saved))
	}
}

func TestEvolveMergesBehavioralMatch(t *testing.T) {
	existing := &models.IncidentSignature{
		SignatureID:     "sig-existing",
		Shape:           "cascade",
		DecayFactor:     0.8,
		OccurrenceCount: 3,
		BehavioralHash:  "hash",
	}
	store := &configurableSignatureStore{
		findByHashResults: []*models.IncidentSignature{existing},
	}
	ev := NewSignatureEvolver(DefaultConfig(), store, nil)

	incoming := &models.IncidentSignature{SignatureID: "sig-incoming", Shape: "cascade"}
	got, err := ev.Evolve(context.Background(), incoming)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SignatureID != "sig-existing" {
		t.Fatalf("expected merge into existing signature, got %q", got.SignatureID)
	}
	if got.OccurrenceCount != 4 {
		t.Fatalf("expected incremented occurrence count, got %d", got.OccurrenceCount)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected exactly one Save call, got %d", len(store.saved))
	}
}
