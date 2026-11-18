package queue

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskSinkEncryptsPayloadAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.ndjson")
	sink, err := NewDiskSink(DiskSinkConfig{
		Path:       path,
		EncryptKey: "queue-secret",
	})
	if err != nil {
		t.Fatalf("new disk sink: %v", err)
	}
	defer func() {
		_ = sink.Close(context.Background())
	}()

	payload := []byte(`{"event_id":"evt-1","message":"secret"}`)
	if err := sink.WriteEvent(context.Background(), payload, nil); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sink file: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("expected persisted queue payload")
	}
	if strings.Contains(string(raw), `"message":"secret"`) || strings.Contains(string(raw), `"event_id":"evt-1"`) {
		t.Fatalf("expected encrypted payload, got plaintext %q", string(raw))
	}
}

func TestDiskSinkRotationRetainsMaxFilesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.ndjson")
	const maxFiles = 3
	sink, err := NewDiskSink(DiskSinkConfig{
		Path:        path,
		MaxFileSize: 1, // force a rotation on every write after the first
		MaxFiles:    maxFiles,
	})
	if err != nil {
		t.Fatalf("new disk sink: %v", err)
	}
	defer func() {
		_ = sink.Close(context.Background())
	}()

	ctx := context.Background()
	for _, marker := range []string{"gen-a", "gen-b", "gen-c", "gen-d", "gen-e"} {
		if err := sink.WriteEvent(ctx, []byte(marker), nil); err != nil {
			t.Fatalf("write event %s: %v", marker, err)
		}
	}

	// After the final write the active file holds "gen-e". The retained
	// backups are .0 (gen-d), .1 (gen-c) and .2 (gen-b); the oldest event,
	// "gen-a", is discarded once more than MaxFiles generations exist.
	want := map[string]string{
		".0": "gen-d",
		".1": "gen-c",
		".2": "gen-b",
	}
	for suffix, marker := range want {
		backup := filepath.Join(dir, "queue"+suffix+".ndjson")
		raw, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("read backup %s: %v", backup, err)
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(marker))
		if !strings.Contains(string(raw), encoded) {
			t.Fatalf("backup %s = %q, want it to contain payload %q", backup, raw, encoded)
		}
	}

	// No backup beyond MaxFiles should ever be created.
	extra := filepath.Join(dir, "queue.3.ndjson")
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup beyond MaxFiles: %s (err=%v)", extra, err)
	}

	// The active file still holds the newest event.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read active file: %v", err)
	}
	if encoded := base64.StdEncoding.EncodeToString([]byte("gen-e")); !strings.Contains(string(raw), encoded) {
		t.Fatalf("active file = %q, want it to contain payload %q", raw, encoded)
	}
}
