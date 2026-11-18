package memory

import (
	"context"
	"errors"
	"testing"

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
