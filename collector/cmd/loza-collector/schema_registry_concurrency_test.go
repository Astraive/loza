package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func publishSchemaEntry(state *collectorState, schemaVersion, eventVersion string, fields []string) *httptest.ResponseRecorder {
	body, err := json.Marshal(schemaEntryRequest{
		SchemaVersion:  schemaVersion,
		EventVersion:   eventVersion,
		RequiredFields: fields,
	})
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/schema/publish", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	state.handleSchemaPublish(rec, req)
	return rec
}

// TestSchemaRegistryConcurrentPublishPreservesEntries covers the race where
// handleSchemaPublish appended to s.cfg.schemaRegistry without a lock:
// concurrent publishes lose updates (or overwrite each other) and the list,
// diff and processor-setup readers can observe a torn slice. Run with -race
// to additionally verify that no unsynchronized access remains.
func TestSchemaRegistryConcurrentPublishPreservesEntries(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.schemaRegistryFile = ""
	cfg.schemaMode = "enforce"
	cfg.schemaRegistry = nil
	state := &collectorState{cfg: cfg}

	const publishers = 6
	const perPublisher = 40
	const readers = 3

	start := make(chan struct{})
	var wg sync.WaitGroup

	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < perPublisher; i++ {
				fields := []string{"id", "ts", "payload"}
				rec := publishSchemaEntry(state, fmt.Sprintf("sv-%d", id), fmt.Sprintf("ev-%d", i), fields)
				if rec.Code != http.StatusOK {
					t.Errorf("publisher %d iteration %d: status %d body %s", id, i, rec.Code, rec.Body.String())
					return
				}
			}
		}(p)
	}

	// These are the three reader paths identified in the finding.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perPublisher*2; i++ {
				listRec := httptest.NewRecorder()
				state.handleSchemaList(listRec, httptest.NewRequest(http.MethodGet, "/schema/list", nil))
				if listRec.Code != http.StatusOK {
					t.Errorf("schema list: status %d body %s", listRec.Code, listRec.Body.String())
					return
				}

				diffBody := `{"schema_version":"sv-0","event_version":"ev-0","required_fields":["id","ts"]}`
				diffRec := httptest.NewRecorder()
				state.handleSchemaDiff(diffRec, httptest.NewRequest(http.MethodPost, "/schema/diff", strings.NewReader(diffBody)))
				if diffRec.Code != http.StatusOK {
					t.Errorf("schema diff: status %d body %s", diffRec.Code, diffRec.Body.String())
					return
				}

				_ = state.convertSchemaRegistry()
			}
		}()
	}

	close(start)
	wg.Wait()

	// All goroutines are done, so a direct read here is safe.
	entries := state.cfg.schemaRegistry
	if got, want := len(entries), publishers*perPublisher; got != want {
		t.Fatalf("registry has %d entries, want %d (concurrent publishes lost updates)", got, want)
	}

	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		key := entry.SchemaVersion + "/" + entry.EventVersion
		if seen[key] {
			t.Errorf("duplicate registry entry %q", key)
		}
		seen[key] = true
	}
	for p := 0; p < publishers; p++ {
		for i := 0; i < perPublisher; i++ {
			key := fmt.Sprintf("sv-%d/ev-%d", p, i)
			if !seen[key] {
				t.Errorf("missing registry entry %q", key)
			}
		}
	}
}

// TestSchemaRegistryConcurrentUpdateIsAtomic covers the in-place element
// write at schema_audit_handlers.go:81. Readers must never observe a partially
// updated RequiredFields slice; the final value must be one of the complete
// field sets that were published.
func TestSchemaRegistryConcurrentUpdateIsAtomic(t *testing.T) {
	cfg := testCollectorConfig()
	cfg.schemaRegistryFile = ""
	cfg.schemaRegistry = nil
	state := &collectorState{cfg: cfg}

	fieldSets := [][]string{
		normalizeFields([]string{"alpha", "beta", "gamma"}),
		normalizeFields([]string{"delta", "epsilon"}),
		normalizeFields([]string{"four", "one", "three", "two"}),
	}

	if rec := publishSchemaEntry(state, "sv", "ev", fieldSets[0]); rec.Code != http.StatusOK {
		t.Fatalf("initial publish: status %d body %s", rec.Code, rec.Body.String())
	}

	const iterations = 60
	start := make(chan struct{})
	var wg sync.WaitGroup

	for _, fields := range fieldSets {
		wg.Add(1)
		go func(fields []string) {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				rec := publishSchemaEntry(state, "sv", "ev", fields)
				if rec.Code != http.StatusOK {
					t.Errorf("update: status %d body %s", rec.Code, rec.Body.String())
					return
				}
			}
		}(fields)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			listRec := httptest.NewRecorder()
			state.handleSchemaList(listRec, httptest.NewRequest(http.MethodGet, "/schema/list", nil))
			if listRec.Code != http.StatusOK {
				t.Errorf("schema list during update: status %d body %s", listRec.Code, listRec.Body.String())
				return
			}
			if entry := state.findSchemaRegistryEntry("sv", "ev"); entry != nil {
				if len(entry.RequiredFields) == 0 {
					t.Errorf("findSchemaRegistryEntry returned empty RequiredFields")
					return
				}
			}
		}
	}()

	close(start)
	wg.Wait()

	entry := state.findSchemaRegistryEntry("sv", "ev")
	if entry == nil {
		t.Fatal("schema entry missing after concurrent updates")
	}
	valid := false
	for _, fields := range fieldSets {
		if slices.Equal(entry.RequiredFields, fields) {
			valid = true
			break
		}
	}
	if !valid {
		t.Fatalf("RequiredFields = %v, want one of %v", entry.RequiredFields, fieldSets)
	}
}
