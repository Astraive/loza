package memory

import (
	"context"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
)

// fakeSignatureStore is a minimal in-memory SignatureStore used to observe
// which signatures the cascading rename callback saves.
type fakeSignatureStore struct {
	sigs  []*models.IncidentSignature
	saved []*models.IncidentSignature
}

func (f *fakeSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	f.saved = append(f.saved, sig)
	return nil
}

func (*fakeSignatureStore) Get(context.Context, string) (*models.IncidentSignature, error) {
	return nil, nil
}

func (f *fakeSignatureStore) List(context.Context, int) ([]*models.IncidentSignature, error) {
	return f.sigs, nil
}

func (*fakeSignatureStore) FindSimilar(context.Context, *models.IncidentSignature, int) ([]*models.SimilarIncident, error) {
	return nil, nil
}

func (*fakeSignatureStore) FindByBehavioralHash(context.Context, string) ([]*models.IncidentSignature, error) {
	return nil, nil
}

func (*fakeSignatureStore) UpdateDecay(context.Context, string, float64) error {
	return nil
}

func (*fakeSignatureStore) ArchiveStale(context.Context, float64) (int, error) {
	return 0, nil
}

func (*fakeSignatureStore) UpdateLastMatched(context.Context, string) error {
	return nil
}

func TestOnAliasRegisteredHonorsValidFrom(t *testing.T) {
	validFrom := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	oldSig := &models.IncidentSignature{
		SignatureID:  "sig-before",
		ServiceRoles: []string{"svc-old", "svc-other"},
		CreatedAt:    validFrom.Add(-24 * time.Hour),
	}
	newSig := &models.IncidentSignature{
		SignatureID:  "sig-after",
		ServiceRoles: []string{"svc-old"},
		CreatedAt:    validFrom.Add(24 * time.Hour),
	}
	boundarySig := &models.IncidentSignature{
		SignatureID:  "sig-boundary",
		ServiceRoles: []string{"svc-old"},
		CreatedAt:    validFrom,
	}

	store := &fakeSignatureStore{sigs: []*models.IncidentSignature{oldSig, newSig, boundarySig}}
	ev := &SignatureEvolver{signatureStore: store}
	ev.onAliasRegistered(context.Background(), "svc-old", "svc-new", validFrom)

	if got := oldSig.ServiceRoles[0]; got != "svc-old" {
		t.Errorf("signature predating validFrom was retroactively renamed: got %q, want %q", got, "svc-old")
	}
	for _, saved := range store.saved {
		if saved.SignatureID == oldSig.SignatureID {
			t.Errorf("signature predating validFrom should not be saved, but %q was", saved.SignatureID)
		}
	}
	if got := newSig.ServiceRoles[0]; got != "svc-new" {
		t.Errorf("signature created after validFrom was not renamed: got %q, want %q", got, "svc-new")
	}
	if got := boundarySig.ServiceRoles[0]; got != "svc-new" {
		t.Errorf("signature created at validFrom was not renamed: got %q, want %q", got, "svc-new")
	}
}

func TestOnAliasRegisteredZeroValidFromRenamesAll(t *testing.T) {
	// A zero validFrom means the alias has always been valid: no signature
	// should be treated as predating it, so every signature is renamed.
	sig := &models.IncidentSignature{
		SignatureID:  "sig",
		ServiceRoles: []string{"svc-old"},
		CreatedAt:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	store := &fakeSignatureStore{sigs: []*models.IncidentSignature{sig}}
	ev := &SignatureEvolver{signatureStore: store}
	ev.onAliasRegistered(context.Background(), "svc-old", "svc-new", time.Time{})

	if got := sig.ServiceRoles[0]; got != "svc-new" {
		t.Errorf("signature was not renamed for an always-valid alias: got %q, want %q", got, "svc-new")
	}
}
