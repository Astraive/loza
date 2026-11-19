package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeRaceReloadConfig rewrites the on-disk config used by reloadCollectorState,
// changing only the mutable schema-version policy. Transport/storage topology
// is intentionally left at defaults so a reload can pick up the new value.
func writeRaceReloadConfig(t *testing.T, path, version string) {
	t.Helper()
	contents := "version: \"1\"\n" +
		"collector:\n" +
		"  addr: \"127.0.0.1:0\"\n" +
		"schema_governance:\n" +
		"  mode: strict\n" +
		"  schema_version: \"" + version + "\"\n" +
		"  event_version: \"" + version + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func versionFromHandler(t *testing.T, state *collectorState) string {
	t.Helper()
	rec := httptest.NewRecorder()
	state.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	var body struct {
		SchemaVersion string `json:"schema_version"`
		EventVersion  string `json:"event_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response %q: %v", rec.Body.String(), err)
	}
	if body.SchemaVersion != body.EventVersion {
		t.Fatalf("torn config observed: schema_version=%q event_version=%q", body.SchemaVersion, body.EventVersion)
	}
	return body.SchemaVersion
}

// TestReloadConfigConcurrentWithRequestHandlers reproduces the reported race:
// a SIGHUP reload swaps state.cfg while request handlers read it. The reader
// here is the real handleVersion handler, so on the unfixed code this test
// fails under -race with a write/read on collectorState.cfg. With the fix,
// both sides go through the mutex-guarded accessor.
func TestReloadConfigConcurrentWithRequestHandlers(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "loza.yaml")
	writeRaceReloadConfig(t, cfgPath, "v1")

	cfg, err := loadCollectorConfigFromArgs([]string{"-c", cfgPath})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	state := &collectorState{cfg: cfg}

	const readers = 8
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				state.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
			}
		}()
	}

	const reloads = 100
	for i := 0; i < reloads; i++ {
		version := "v1"
		if i%2 == 1 {
			version = "v2"
		}
		writeRaceReloadConfig(t, cfgPath, version)
		if err := reloadCollectorState(state); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("reload %d: %v", i, err)
		}
	}

	// Publish a known final value, then stop the readers. Readers must observe
	// a complete, consistent config after the swap.
	writeRaceReloadConfig(t, cfgPath, "v3")
	if err := reloadCollectorState(state); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("final reload: %v", err)
	}
	close(stop)
	wg.Wait()

	if got := versionFromHandler(t, state); got != "v3" {
		t.Fatalf("version after final reload = %q, want v3", got)
	}
	if got := state.config().schemaSchemaVersion; got != "v3" {
		t.Fatalf("config snapshot version = %q, want v3", got)
	}
}

// TestConfigSnapshotIsolation verifies that a reader holding a snapshot is
// unaffected by a later config swap, i.e. readers see an immutable value.
func TestConfigSnapshotIsolation(t *testing.T) {
	state := &collectorState{}
	state.setConfig(collectorConfig{schemaSchemaVersion: "v1", schemaEventVersion: "e1"})

	before := state.config()
	state.setConfig(collectorConfig{schemaSchemaVersion: "v2", schemaEventVersion: "e2"})

	if before.schemaSchemaVersion != "v1" || before.schemaEventVersion != "e1" {
		t.Fatalf("snapshot changed after swap: %+v", before)
	}
	after := state.config()
	if after.schemaSchemaVersion != "v2" || after.schemaEventVersion != "e2" {
		t.Fatalf("new snapshot not published: %+v", after)
	}
	if !strings.EqualFold(after.schemaSchemaVersion, "V2") {
		t.Fatalf("unexpected schema version %q", after.schemaSchemaVersion)
	}
}
