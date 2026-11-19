package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	collectorconfig "github.com/astraive/loza/collector/internal/config"
)

// TestSchemaRegistryFileSaveDoesNotReuseFixedTempPath verifies that a save does
// not use a shared, deterministic temporary filename. If it did, a stale
// <path>.tmp left by another save would be truncated and renamed over the
// registry, so its contents must survive an unrelated save untouched.
func TestSchemaRegistryFileSaveDoesNotReuseFixedTempPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	fixedTmp := path + ".tmp"
	const sentinel = "left-behind-by-another-save"
	if err := os.WriteFile(fixedTmp, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed fixed temp path: %v", err)
	}

	entries := []collectorconfig.SchemaRegistryEntry{{
		SchemaVersion:  "v2",
		EventVersion:   "v1",
		RequiredFields: []string{"event_id"},
	}}
	if err := saveSchemaRegistryFile(path, entries, ""); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	got, err := os.ReadFile(fixedTmp)
	if err != nil {
		t.Fatalf("read fixed temp path after save: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("save reused the shared temp path %q; got %q", fixedTmp, got)
	}
}

// TestSchemaRegistryFileConcurrentSaves reproduces concurrent handleSchemaPublish
// style saves to the same registry file. Each save must be independent: no save
// may fail and the final file must contain exactly one complete registry entry.
func TestSchemaRegistryFileConcurrentSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	const writers = 16
	const iterations = 40

	// A sizeable payload widens the window in which two writes to a shared temp
	// file would interleave and corrupt each other.
	fields := make([]string, 256)
	for i := range fields {
		fields[i] = fmt.Sprintf("field_%03d", i)
	}

	errCh := make(chan error, writers*iterations)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				entries := []collectorconfig.SchemaRegistryEntry{{
					SchemaVersion:  fmt.Sprintf("v%d", i),
					EventVersion:   "v1",
					RequiredFields: fields,
				}}
				if err := saveSchemaRegistryFile(path, entries, ""); err != nil {
					errCh <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent save failed: %v", err)
	}

	loaded, err := loadPersistedSchemaRegistry(path, "")
	if err != nil {
		t.Fatalf("registry corrupted by concurrent saves: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one registry entry after concurrent saves, got %d", len(loaded))
	}
}
