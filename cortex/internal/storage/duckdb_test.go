package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
)

func newTestTopologyStore(t *testing.T) TopologyStore {
	t.Helper()
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "cortex.duckdb"))
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = stor.Close() })
	if err := stor.Init(context.Background()); err != nil {
		t.Fatalf("init duckdb: %v", err)
	}
	return stor.Topology()
}

// Regression for MASS-0357: an unparsable timestamp must be reported as an
// error instead of silently becoming the zero time and querying with it.
func TestDuckDBGetAliasRejectsInvalidTimestamp(t *testing.T) {
	topo := newTestTopologyStore(t)
	ctx := context.Background()

	for _, ts := range []string{"", "not-a-timestamp", "2024-01-01"} {
		got, err := topo.GetAlias(ctx, "api", ts)
		if err == nil {
			t.Fatalf("GetAlias(%q) = %+v, want an error for an invalid timestamp", ts, got)
		}
		if got != nil {
			t.Errorf("GetAlias(%q) returned a non-nil alias alongside the error: %+v", ts, got)
		}
		if !strings.Contains(err.Error(), "invalid timestamp") {
			t.Errorf("GetAlias(%q) error %q does not describe the bad input", ts, err)
		}
		var parseErr *time.ParseError
		if !errors.As(err, &parseErr) {
			t.Errorf("GetAlias(%q) error %q does not wrap the time.Parse error", ts, err)
		}
	}
}

// The parse check must not break resolution with a well-formed timestamp.
func TestDuckDBGetAliasWithValidTimestamp(t *testing.T) {
	topo := newTestTopologyStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	alias := &models.ServiceAlias{
		ID:        "api->gateway",
		Alias:     "api",
		Canonical: "gateway",
		ValidFrom: now.Add(-time.Hour),
	}
	if err := topo.SaveAlias(ctx, alias); err != nil {
		t.Fatalf("SaveAlias: %v", err)
	}

	got, err := topo.GetAlias(ctx, "api", now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("GetAlias with valid timestamp: %v", err)
	}
	if got == nil || got.Canonical != "gateway" {
		t.Fatalf("GetAlias = %+v, want canonical %q", got, "gateway")
	}
}

func TestDuckDBFindSimilarRanksFallbackBeforeLimit(t *testing.T) {
	ctx := context.Background()
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "similarity.duckdb"))
	if err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() {
		if err := stor.Close(); err != nil {
			t.Errorf("close DuckDB: %v", err)
		}
	})
	if err := stor.Init(ctx); err != nil {
		t.Fatalf("initialize DuckDB: %v", err)
	}

	signatures := stor.Signatures()
	query := &models.IncidentSignature{
		SignatureID: "query",
		Shape:       "api_timeout",
		Symptoms:    []models.SymptomType{models.SymptomTypeTimeout},
	}
	for _, candidate := range []*models.IncidentSignature{
		{
			SignatureID:     "popular-weaker",
			Shape:           "api_timeout_related",
			Symptoms:        []models.SymptomType{models.SymptomTypeTimeout},
			OccurrenceCount: 100,
		},
		{
			SignatureID:     "exact",
			Shape:           query.Shape,
			Symptoms:        []models.SymptomType{models.SymptomTypeTimeout},
			OccurrenceCount: 1,
		},
	} {
		if err := signatures.Save(ctx, candidate); err != nil {
			t.Fatalf("save signature %q: %v", candidate.SignatureID, err)
		}
	}

	ranked, err := signatures.FindSimilar(ctx, query, 2)
	if err != nil {
		t.Fatalf("FindSimilar limit 2: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("FindSimilar limit 2 returned %d results, want 2", len(ranked))
	}
	if ranked[0].IncidentID != "exact" {
		t.Errorf("first result = %q, want exact signature", ranked[0].IncidentID)
	}
	if ranked[0].Similarity <= ranked[1].Similarity {
		t.Errorf("results are not ranked by similarity: first=%v second=%v", ranked[0].Similarity, ranked[1].Similarity)
	}

	top, err := signatures.FindSimilar(ctx, query, 1)
	if err != nil {
		t.Fatalf("FindSimilar limit 1: %v", err)
	}
	if len(top) != 1 || top[0].IncidentID != "exact" {
		t.Errorf("FindSimilar limit 1 = %+v, want only exact signature", top)
	}
}

func TestDuckDBFindSimilarReturnsSubthresholdCandidates(t *testing.T) {
	ctx := context.Background()
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "subthreshold.duckdb"))
	if err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() {
		if err := stor.Close(); err != nil {
			t.Errorf("close DuckDB: %v", err)
		}
	})
	if err := stor.Init(ctx); err != nil {
		t.Fatalf("initialize DuckDB: %v", err)
	}

	signatures := stor.Signatures()
	query := &models.IncidentSignature{SignatureID: "query", Shape: "api_timeout"}
	candidate := &models.IncidentSignature{SignatureID: "subthreshold", Shape: query.Shape}
	if err := signatures.Save(ctx, candidate); err != nil {
		t.Fatalf("save signature: %v", err)
	}

	matches, err := signatures.FindSimilar(ctx, query, 5)
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(matches) != 1 || matches[0].IncidentID != candidate.SignatureID {
		t.Fatalf("FindSimilar = %+v, want the subthreshold candidate", matches)
	}
	if matches[0].Similarity != 0.4 {
		t.Errorf("subthreshold similarity = %v, want 0.4", matches[0].Similarity)
	}
}

func TestDuckDBFindSimilarSQLRetainsSubthresholdCandidate(t *testing.T) {
	ctx := context.Background()
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "sql-subthreshold.duckdb"))
	if err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() {
		if err := stor.Close(); err != nil {
			t.Errorf("close DuckDB: %v", err)
		}
	})
	if err := stor.Init(ctx); err != nil {
		t.Fatalf("initialize DuckDB: %v", err)
	}

	signatures := stor.Signatures()
	queryVector := make([]float64, 768)
	queryVector[0] = 1
	query := &models.IncidentSignature{SignatureID: "query", FeatureVector: queryVector}

	insertEmbedding := func(signatureID, secondValue string) {
		values := make([]string, 768)
		for i := range values {
			values[i] = "0"
		}
		values[0] = "1"
		values[1] = secondValue
		embedding := "CAST([" + strings.Join(values, ",") + "] AS FLOAT[768])"
		_, err := stor.db.ExecContext(ctx,
			"INSERT INTO incident_signatures (signature_id, shape, embedding) VALUES (?, ?, "+embedding+")",
			signatureID, "api_timeout",
		)
		if err != nil {
			t.Fatalf("insert embedding for %q: %v", signatureID, err)
		}
	}
	insertEmbedding("high", "0")
	insertEmbedding("below-sql-cutoff", "4")

	matches, err := signatures.FindSimilar(ctx, query, 5)
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("FindSimilar returned %d results, want 2: %+v", len(matches), matches)
	}
	if matches[0].IncidentID != "high" || matches[1].IncidentID != "below-sql-cutoff" {
		t.Fatalf("FindSimilar order = [%q, %q], want [high, below-sql-cutoff]", matches[0].IncidentID, matches[1].IncidentID)
	}
	if matches[1].Similarity <= 0.2 || matches[1].Similarity >= 0.3 {
		t.Errorf("below-cutoff similarity = %v, want between 0.2 and 0.3", matches[1].Similarity)
	}
}
