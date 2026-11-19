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
