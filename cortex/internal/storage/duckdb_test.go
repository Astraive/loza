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

func TestDuckDBRemediationListBySignatureRoundsFractionalAverage(t *testing.T) {
	ctx := context.Background()
	stor, err := NewDuckDBStorage(filepath.Join(t.TempDir(), "remediation.duckdb"))
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

	now := time.Now().UTC()
	incidentID := "incident-1"
	if err := stor.Incidents().Save(ctx, &models.Incident{
		ID:             incidentID,
		Timestamp:      now,
		Status:         "resolved",
		Severity:       "medium",
		PrimaryService: "api",
	}); err != nil {
		t.Fatalf("save incident: %v", err)
	}

	remediations := stor.Remediations()
	feedback := stor.Feedback()
	for _, sample := range []struct {
		remediationID string
		duration      int64
	}{
		{remediationID: "remediation-10", duration: 10},
		{remediationID: "remediation-11", duration: 11},
	} {
		if err := remediations.Save(ctx, &models.Remediation{
			RemediationID: sample.remediationID,
			IncidentID:    incidentID,
			SignatureID:   "signature-1",
			Action:        "restart",
			Timestamp:     now,
		}); err != nil {
			t.Fatalf("save remediation %q: %v", sample.remediationID, err)
		}
		if err := feedback.Save(ctx, &models.RemediationFeedback{
			FeedbackID:    "feedback-" + sample.remediationID,
			RemediationID: sample.remediationID,
			IncidentID:    incidentID,
			OutcomeCode:   200,
			TimeToResolve: sample.duration,
			Timestamp:     now,
		}); err != nil {
			t.Fatalf("save feedback for %q: %v", sample.remediationID, err)
		}
	}

	stats, err := remediations.ListBySignature(ctx, "signature-1", 10)
	if err != nil {
		t.Fatalf("ListBySignature: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("ListBySignature returned %d rows, want 1", len(stats))
	}
	if stats[0].AvgTimeToResolve != 11 {
		t.Errorf("AvgTimeToResolve = %d, want 11 after rounding 10.5 to nearest second", stats[0].AvgTimeToResolve)
	}
}
