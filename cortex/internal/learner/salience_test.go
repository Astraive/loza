package learner

import (
	"context"
	"math"
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

// countingSalienceStore is an in-memory SalienceStore that preserves the score
// and sample count across Get/Save calls, mirroring a real persistent store.
type countingSalienceStore struct {
	scores map[string]*models.SalienceScore
}

func newCountingSalienceStore() *countingSalienceStore {
	return &countingSalienceStore{scores: make(map[string]*models.SalienceScore)}
}

func (s *countingSalienceStore) Save(_ context.Context, score *models.SalienceScore) error {
	stored := *score
	s.scores[score.EventType] = &stored
	return nil
}

func (s *countingSalienceStore) Get(_ context.Context, eventType string) (*models.SalienceScore, error) {
	if score, ok := s.scores[eventType]; ok {
		return score, nil
	}
	// Unknown event types start neutral with no observations.
	return &models.SalienceScore{EventType: eventType, Score: 0.5, SampleCount: 0}, nil
}

func (s *countingSalienceStore) List(_ context.Context, limit int) ([]*models.SalienceScore, error) {
	return nil, nil
}

func TestRecordOutcomeAccumulatesSampleCount(t *testing.T) {
	ctx := context.Background()
	store := newCountingSalienceStore()
	tracker := NewSalienceTracker(store)

	// alpha=0.1, default=0.5, success reward=1.0:
	// 0.5 -> 0.55 -> 0.595 -> 0.6355, with one new sample per outcome.
	wantScores := []float64{0.55, 0.595, 0.6355}
	for i, wantCount := range []int{1, 2, 3} {
		if err := tracker.RecordOutcome(ctx, []string{"http.error"}, 200); err != nil {
			t.Fatalf("record outcome %d: %v", i+1, err)
		}

		got := store.scores["http.error"]
		if got == nil {
			t.Fatalf("record outcome %d: no salience stored", i+1)
		}
		if got.SampleCount != wantCount {
			t.Fatalf("record outcome %d: sample count = %d, want %d", i+1, got.SampleCount, wantCount)
		}
		if math.Abs(got.Score-wantScores[i]) > 1e-9 {
			t.Fatalf("record outcome %d: score = %v, want %v", i+1, got.Score, wantScores[i])
		}
	}

	if got := tracker.GetSalience(ctx, "http.error"); math.Abs(got-0.6355) > 1e-9 {
		t.Fatalf("GetSalience = %v, want %v", got, 0.6355)
	}
}
