package storage

import (
	"math"
	"testing"

	"github.com/astraive/loza/cortex/internal/models"
)

func TestComputeSimilarityUsesNormalizedCosine(t *testing.T) {
	a := &models.IncidentSignature{
		Shape:         "deploy->timeout",
		Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
		FeatureVector: []float64{1, 1},
	}
	b := &models.IncidentSignature{
		Shape:         "deploy->timeout",
		Symptoms:      []models.SymptomType{models.SymptomTypeTimeout},
		FeatureVector: []float64{1, 1},
	}

	gotDuckDB := computeSimilarity(a, b)
	gotPostgres := computeSimilarityPostgres(a, b)

	if math.Abs(gotDuckDB-1.0) > 0.0001 {
		t.Fatalf("expected duckdb similarity near 1, got %v", gotDuckDB)
	}
	if math.Abs(gotPostgres-1.0) > 0.0001 {
		t.Fatalf("expected postgres similarity near 1, got %v", gotPostgres)
	}
}

// TestComputeSimilaritySymptomUnionIntersection locks in the symptom Jaccard
// calculation. The previous dead loop (`for k := range bSymptoms { bSymptoms[k] = true }`)
// was a copy/paste of the real union-building loop and had no effect. These cases
// exercise the union/intersection path directly (empty shape and feature vector so
// symptom similarity is the only contributor, scaled by its 0.3 weight) to guard
// against that logic being dropped or corrupted when the dead code is removed.
func TestComputeSimilaritySymptomUnionIntersection(t *testing.T) {
	tests := []struct {
		name      string
		aSymptoms []models.SymptomType
		bSymptoms []models.SymptomType
		want      float64
	}{
		{
			name:      "half overlap",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeErrorRate, models.SymptomTypeLatencySpike},
			bSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeErrorRate, models.SymptomTypeMemoryLeak},
			want:      0.3 * (2.0 / 4.0),
		},
		{
			name:      "full overlap",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeErrorRate},
			bSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeErrorRate},
			want:      0.3 * 1.0,
		},
		{
			name:      "no overlap",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout},
			bSymptoms: []models.SymptomType{models.SymptomTypeMemoryLeak},
			want:      0.0,
		},
		{
			name:      "duplicate symptoms deduplicated",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeTimeout, models.SymptomTypeErrorRate},
			bSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeLatencySpike},
			want:      0.3 * (1.0 / 3.0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &models.IncidentSignature{Symptoms: tt.aSymptoms}
			b := &models.IncidentSignature{Symptoms: tt.bSymptoms}

			gotPostgres := computeSimilarityPostgres(a, b)
			if math.Abs(gotPostgres-tt.want) > 1e-9 {
				t.Errorf("computeSimilarityPostgres() = %v, want %v", gotPostgres, tt.want)
			}

			gotDuckDB := computeSimilarity(a, b)
			if math.Abs(gotDuckDB-tt.want) > 1e-9 {
				t.Errorf("computeSimilarity() = %v, want %v", gotDuckDB, tt.want)
			}
		})
	}
}

// TestComputeSimilaritySymptomOverlap guards the symptom-set similarity
// computation where a redundant `bSymptoms[k] = true` loop used to sit. With
// shape and feature vectors left empty, the overall score reduces to
// 0.3 * Jaccard(aSymptoms, bSymptoms), so any change to how the symptom maps,
// union, or intersection are built is caught here.
func TestComputeSimilaritySymptomOverlap(t *testing.T) {
	tests := []struct {
		name      string
		aSymptoms []models.SymptomType
		bSymptoms []models.SymptomType
		want      float64
	}{
		{
			name:      "identical symptom sets",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeLatencySpike},
			bSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeLatencySpike},
			want:      0.3,
		},
		{
			name:      "partial overlap",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeLatencySpike},
			bSymptoms: []models.SymptomType{models.SymptomTypeLatencySpike, models.SymptomTypeErrorRate},
			want:      0.1, // intersection 1 / union 3 => 0.3 * (1/3)
		},
		{
			name:      "disjoint symptom sets",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout},
			bSymptoms: []models.SymptomType{models.SymptomTypeMemoryLeak},
			want:      0,
		},
		{
			name:      "empty b symptom set",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout},
			bSymptoms: nil,
			want:      0,
		},
		{
			name:      "duplicate symptoms are deduplicated",
			aSymptoms: []models.SymptomType{models.SymptomTypeTimeout},
			bSymptoms: []models.SymptomType{models.SymptomTypeTimeout, models.SymptomTypeTimeout},
			want:      0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &models.IncidentSignature{Symptoms: tt.aSymptoms}
			b := &models.IncidentSignature{Symptoms: tt.bSymptoms}

			for name, got := range map[string]float64{
				"duckdb":   computeSimilarity(a, b),
				"postgres": computeSimilarityPostgres(a, b),
			} {
				if math.Abs(got-tt.want) > 0.0001 {
					t.Errorf("%s similarity: got %v, want %v", name, got, tt.want)
				}
			}
		})
	}
}
