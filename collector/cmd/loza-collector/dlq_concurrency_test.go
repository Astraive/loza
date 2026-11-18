package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTestDLQ(t *testing.T, path string, records ...string) {
	t.Helper()
	var content strings.Builder
	for _, record := range records {
		content.WriteString(record)
		content.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatalf("write DLQ: %v", err)
	}
}

func readTestDLQIDs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read DLQ: %v", err)
	}
	ids := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("corrupt DLQ line %q: %v", line, err)
		}
		ids = append(ids, fmt.Sprint(record["dlq_id"]))
	}
	return ids
}

// TestHandleDLQDeleteSerializesWithConcurrentWriter is a deterministic
// reproduction of the read-modify-write race: a delete must not run while
// another DLQ mutation is in progress, and must operate on fresh state once
// it acquires the lock.
func TestHandleDLQDeleteSerializesWithConcurrentWriter(t *testing.T) {
	dlqPath := filepath.Join(t.TempDir(), "dlq.ndjson")
	writeTestDLQ(t, dlqPath,
		`{"dlq_id":"a","event_id":"a"}`,
		`{"dlq_id":"b","event_id":"b"}`,
	)
	state := &collectorState{cfg: collectorConfig{dlqPath: dlqPath}}

	state.dlqMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodDelete, "/control/dlq/a", nil)
		req.SetPathValue("id", "a")
		rec := httptest.NewRecorder()
		state.handleDLQDelete(rec, req)
	}()

	select {
	case <-done:
		state.dlqMu.Unlock()
		t.Fatal("DLQ delete completed while another mutation held the DLQ lock")
	case <-time.After(100 * time.Millisecond):
	}

	// Mutate the DLQ while holding the lock. The pending delete must see this
	// fresh state once it is allowed to proceed.
	f, err := os.OpenFile(dlqPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		state.dlqMu.Unlock()
		t.Fatalf("open DLQ: %v", err)
	}
	if _, err := f.WriteString(`{"dlq_id":"c","event_id":"c"}` + "\n"); err != nil {
		_ = f.Close()
		state.dlqMu.Unlock()
		t.Fatalf("append DLQ: %v", err)
	}
	_ = f.Close()
	state.dlqMu.Unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DLQ delete did not complete after the lock was released")
	}

	ids := readTestDLQIDs(t, dlqPath)
	want := map[string]bool{"b": true, "c": true}
	if len(ids) != len(want) {
		t.Fatalf("expected %d remaining records, got %d (%v)", len(want), len(ids), ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected record %q remaining: %v", id, ids)
		}
	}
}

// TestWriteDLQRecordsUsesUniqueTempFilesUnderConcurrency guards against the
// shared fixed temp path: concurrent rewrites must leave one complete, valid
// DLQ and no stray temp files.
func TestWriteDLQRecordsUsesUniqueTempFilesUnderConcurrency(t *testing.T) {
	dir := t.TempDir()
	dlqPath := filepath.Join(dir, "dlq.ndjson")
	state := &collectorState{cfg: collectorConfig{dlqPath: dlqPath}}

	const writers = 32
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			records := []map[string]any{{"dlq_id": fmt.Sprintf("id-%d", i)}}
			if err := state.writeDLQRecords(records); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	records, err := state.readDLQRecords()
	if err != nil {
		t.Fatalf("read DLQ: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly one complete DLQ record, got %d: %v", len(records), records)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "dlq.ndjson.tmp*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("leftover temp files: %v", leftovers)
	}
}

// TestHandleDLQDeleteConcurrentDistinctDeletes verifies that deletes of
// distinct records issued at the same time are all preserved.
func TestHandleDLQDeleteConcurrentDistinctDeletes(t *testing.T) {
	dlqPath := filepath.Join(t.TempDir(), "dlq.ndjson")
	state := &collectorState{cfg: collectorConfig{dlqPath: dlqPath}}

	const count = 24
	records := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		records = append(records, map[string]any{"dlq_id": fmt.Sprintf("id-%d", i)})
	}
	if err := state.writeDLQRecords(records); err != nil {
		t.Fatalf("seed DLQ: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("id-%d", i)
			req := httptest.NewRequest(http.MethodDelete, "/control/dlq/"+id, nil)
			req.SetPathValue("id", id)
			rec := httptest.NewRecorder()
			state.handleDLQDelete(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("delete %s: status %d body %s", id, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()

	remaining, err := state.readDLQRecords()
	if err != nil {
		t.Fatalf("read DLQ: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected all %d records deleted, got %d remaining", count, len(remaining))
	}
}
