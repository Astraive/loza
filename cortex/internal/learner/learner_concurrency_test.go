package learner

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

// concurrentSignatureStore behaves like the real SQL stores: Get returns a
// freshly deserialized copy of the persisted row and Save persists a copy.
// That is what makes a read-modify-write sequence lose updates when it is not
// serialized — two callers can both read the same counter value before either
// writes its result back.
type concurrentSignatureStore struct {
	fakeSignatureStoreForLearner
	mu sync.Mutex
}

func cloneSignature(sig *models.IncidentSignature) *models.IncidentSignature {
	clone := *sig
	clone.ServiceRoles = append([]string(nil), sig.ServiceRoles...)
	clone.Symptoms = append([]models.SymptomType(nil), sig.Symptoms...)
	clone.Remediation = append([]string(nil), sig.Remediation...)
	clone.FeatureVector = append([]float64(nil), sig.FeatureVector...)
	clone.FeatureWeights = append([]float64(nil), sig.FeatureWeights...)
	return &clone
}

func (s *concurrentSignatureStore) Get(_ context.Context, id string) (*models.IncidentSignature, error) {
	s.mu.Lock()
	sig, ok := s.sigs[id]
	s.mu.Unlock()
	if !ok {
		return nil, errors.New("not found")
	}
	// Yield between the read and the caller's write to widen the race window
	// so the lost-update repro is reliable even on a single CPU.
	runtime.Gosched()
	return cloneSignature(sig), nil
}

func (s *concurrentSignatureStore) Save(_ context.Context, sig *models.IncidentSignature) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sigs[sig.SignatureID] = cloneSignature(sig)
	return nil
}

func (s *concurrentSignatureStore) occurrenceCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sigs[id].OccurrenceCount
}

type concurrentRemediationStore struct {
	mu  sync.Mutex
	rem map[string]*models.Remediation
}

func (s *concurrentRemediationStore) Save(_ context.Context, rem *models.Remediation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rem[rem.RemediationID] = rem
	return nil
}

func (s *concurrentRemediationStore) Get(_ context.Context, id string) (*models.Remediation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rem, ok := s.rem[id]; ok {
		return rem, nil
	}
	return nil, errors.New("not found")
}

func (*concurrentRemediationStore) ListByIncident(context.Context, string) ([]*models.Remediation, error) {
	return nil, nil
}

func (*concurrentRemediationStore) ListBySignature(context.Context, string, int) ([]*models.RemediationStats, error) {
	return nil, nil
}

type concurrentFeedbackStore struct {
	mu    sync.Mutex
	byRem map[string][]*models.RemediationFeedback
}

func (s *concurrentFeedbackStore) Save(_ context.Context, fb *models.RemediationFeedback) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byRem[fb.RemediationID] = append(s.byRem[fb.RemediationID], fb)
	return nil
}

func (s *concurrentFeedbackStore) GetByRemediation(_ context.Context, id string) ([]*models.RemediationFeedback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*models.RemediationFeedback(nil), s.byRem[id]...), nil
}

func (*concurrentFeedbackStore) GetSuccessRate(context.Context, string, string) (float64, error) {
	return 0, nil
}

func newConcurrentLearner(signatureID string) (*Learner, *concurrentSignatureStore) {
	sigStore := &concurrentSignatureStore{
		fakeSignatureStoreForLearner: fakeSignatureStoreForLearner{
			sigs: map[string]*models.IncidentSignature{
				signatureID: {SignatureID: signatureID},
			},
		},
	}
	return NewLearner(nil, nil, sigStore), sigStore
}

func runConcurrently(t *testing.T, workers int, fn func(int)) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
}

func TestConcurrentReinforcePatternPreservesOccurrenceCount(t *testing.T) {
	const workers = 64
	l, sigStore := newConcurrentLearner("sig-reinforce")

	runConcurrently(t, workers, func(int) {
		if err := l.ReinforcePattern(context.Background(), "sig-reinforce", "restart", 10); err != nil {
			t.Errorf("ReinforcePattern: %v", err)
		}
	})

	if got := sigStore.occurrenceCount("sig-reinforce"); got != workers {
		t.Fatalf("expected occurrence count %d after %d concurrent reinforcements, got %d", workers, workers, got)
	}
}

func TestConcurrentRecordFeedbackPreservesOccurrenceCount(t *testing.T) {
	const workers = 64
	const signatureID = "sig-feedback"
	remStore := &concurrentRemediationStore{rem: make(map[string]*models.Remediation)}
	fbStore := &concurrentFeedbackStore{byRem: make(map[string][]*models.RemediationFeedback)}
	sigStore := &concurrentSignatureStore{
		fakeSignatureStoreForLearner: fakeSignatureStoreForLearner{
			sigs: map[string]*models.IncidentSignature{
				signatureID: {SignatureID: signatureID},
			},
		},
	}
	l := NewLearner(remStore, fbStore, sigStore)

	for i := 0; i < workers; i++ {
		remID := fmtRemediationID(i)
		remStore.rem[remID] = &models.Remediation{RemediationID: remID, SignatureID: signatureID}
	}

	runConcurrently(t, workers, func(i int) {
		fb := &models.RemediationFeedback{
			RemediationID:   fmtRemediationID(i),
			OutcomeCode:     200,
			OutcomeCategory: "success",
			TimeToResolve:   10,
		}
		if err := l.RecordFeedback(context.Background(), fb); err != nil {
			t.Errorf("RecordFeedback: %v", err)
		}
	})

	if got := sigStore.occurrenceCount(signatureID); got != workers {
		t.Fatalf("expected occurrence count %d after %d concurrent feedback records, got %d", workers, workers, got)
	}
}

func fmtRemediationID(i int) string {
	return "rem-" + strconv.Itoa(i)
}
