package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	collectorconfig "github.com/astraive/loza/collector/internal/config"
)

// cloneSchemaRegistry makes an independent copy so assertions cannot be
// satisfied by aliased slices.
func cloneSchemaRegistry(entries []collectorconfig.SchemaRegistryEntry) []collectorconfig.SchemaRegistryEntry {
	out := make([]collectorconfig.SchemaRegistryEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].RequiredFields = append([]string(nil), entry.RequiredFields...)
	}
	return out
}

// TestHandleSchemaPublishSaveFailureDoesNotMutateRegistry reproduces MASS-0120:
// a failed save must not leave the in-memory registry mutated (memory/disk
// divergence that silently loses the "published" schema on restart).
func TestHandleSchemaPublishSaveFailureDoesNotMutateRegistry(t *testing.T) {
	original := []collectorconfig.SchemaRegistryEntry{
		{SchemaVersion: "v1", EventVersion: "v1", RequiredFields: []string{"event_id"}},
	}

	cases := []struct {
		name string
		body string
	}{
		{
			name: "update existing entry",
			body: `{"schema_version":"v1","event_version":"v1","required_fields":["event_id","timestamp"]}`,
		},
		{
			name: "append new entry",
			body: `{"schema_version":"v9","event_version":"v1","required_fields":["event_id"]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")

			// Persist the original registry to disk so we can assert it is
			// untouched after the failed publish.
			originalData, err := json.MarshalIndent(original, "", "  ")
			if err != nil {
				t.Fatalf("marshal original registry: %v", err)
			}
			if err := os.WriteFile(regPath, originalData, 0o600); err != nil {
				t.Fatalf("write original registry: %v", err)
			}

			// Force saveSchemaRegistryFile to fail: it writes to path+".tmp",
			// which cannot be a directory.
			if err := os.Mkdir(regPath+".tmp", 0o755); err != nil {
				t.Fatalf("block tmp file: %v", err)
			}

			cfg := testCollectorConfig()
			cfg.storageEncryptionKey = ""
			cfg.schemaRegistryFile = regPath
			cfg.schemaRegistry = cloneSchemaRegistry(original)

			state := &collectorState{cfg: cfg}
			state.ready.Store(true)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/schema/publish", strings.NewReader(tc.body))
			state.handleSchemaPublish(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 on save failure, got %d body=%s", rec.Code, rec.Body.String())
			}
			if !reflect.DeepEqual(state.cfg.schemaRegistry, original) {
				t.Fatalf("in-memory registry mutated on save failure: got %#v, want %#v", state.cfg.schemaRegistry, original)
			}

			diskData, err := os.ReadFile(regPath)
			if err != nil {
				t.Fatalf("read persisted registry: %v", err)
			}
			if string(diskData) != string(originalData) {
				t.Fatalf("persisted registry changed on save failure: got %s, want %s", diskData, originalData)
			}
		})
	}
}

// TestHandleSchemaPublishCommitsAfterSuccessfulSave verifies the happy path
// still publishes and persists the registry.
func TestHandleSchemaPublishCommitsAfterSuccessfulSave(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	cfg := testCollectorConfig()
	cfg.storageEncryptionKey = ""
	cfg.schemaRegistryFile = regPath
	cfg.schemaRegistry = nil

	state := &collectorState{cfg: cfg}
	state.ready.Store(true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/schema/publish", strings.NewReader(`{"schema_version":"v2","event_version":"v1","required_fields":["event_id","timestamp"]}`))
	state.handleSchemaPublish(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	want := []collectorconfig.SchemaRegistryEntry{
		{SchemaVersion: "v2", EventVersion: "v1", RequiredFields: []string{"event_id", "timestamp"}},
	}
	if !reflect.DeepEqual(state.cfg.schemaRegistry, want) {
		t.Fatalf("registry not committed: got %#v, want %#v", state.cfg.schemaRegistry, want)
	}

	persisted, err := loadPersistedSchemaRegistry(regPath, "")
	if err != nil {
		t.Fatalf("load persisted registry: %v", err)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted registry mismatch: got %#v, want %#v", persisted, want)
	}
}
