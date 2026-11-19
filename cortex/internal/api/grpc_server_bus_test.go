package api

import (
	"path/filepath"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/redaction"
	"github.com/astraive/loza/cortex/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestNewGRPCServerAppliesConfiguredSubscriberBufferSize verifies that the
// EventBusConfig.SubscriberBufferSize value reaches the event bus built by
// NewGRPCServer. Publish drops events once a subscriber is full, so after
// publishing more events than the configured size the number of buffered
// events must equal that configured size (not the hardcoded default of 128).
func TestNewGRPCServerAppliesConfiguredSubscriberBufferSize(t *testing.T) {
	cfg, err := config.Default()
	require.NoError(t, err)
	cfg.Storage.Backend = "duckdb"
	cfg.Storage.DuckDB.Path = filepath.Join(t.TempDir(), "cortex.duckdb")
	cfg.Collector.SourceOfTruth = false
	const bufferSize = 5
	cfg.EventBus.SubscriberBufferSize = bufferSize

	stor, err := storage.NewStorage(cfg)
	require.NoError(t, err)
	defer stor.Close()

	srv := NewGRPCServer(cfg, stor, redaction.Config{})

	ch := srv.bus.Subscribe("")
	defer srv.bus.Unsubscribe("", ch)

	// Publish well above the configured size and above what a default-size
	// buffer would retain for a smaller configuration.
	for i := 0; i < bufferSize+10; i++ {
		srv.bus.Publish(&models.Event{})
	}

	if got := len(ch); got != bufferSize {
		t.Fatalf("expected %d buffered events from configured buffer size, got %d", bufferSize, got)
	}
}
