package processing

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// partialWriteFile simulates an O_APPEND file where the first Write persists
// only a prefix of the buffer and reports an error, while later Writes persist
// everything they are given. This models a short write caused by transient
// pressure (for example, a full disk) on a DLQ/quarantine file.
type partialWriteFile struct {
	mu        sync.Mutex
	writeLens []int
	data      []byte
}

func (f *partialWriteFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeLens = append(f.writeLens, len(p))
	if len(f.writeLens) == 1 {
		n := len(p) / 2
		f.data = append(f.data, p[:n]...)
		return n, errors.New("simulated short write")
	}
	f.data = append(f.data, p...)
	return len(p), nil
}

func (f *partialWriteFile) Close() error { return nil }

func TestWriteDLQPartialWriteDoesNotDuplicate(t *testing.T) {
	f := &partialWriteFile{}
	var successCount, failCount int64
	p := &Processor{
		cfg: Config{
			DLQEnabled:     true,
			OnDLQWrite:     func(n int64) { successCount += n },
			OnDLQWriteFail: func(n int64) { failCount += n },
		},
		dlqFile: f,
	}

	raw := []byte(`{"event":"partial-write"}`)
	p.writeDLQ(raw, errors.New("delivery failed"))

	if len(f.writeLens) < 2 {
		t.Fatalf("expected a retry after the short write, got %d write calls", len(f.writeLens))
	}
	// The retry must write only the suffix that was not persisted by the
	// partial first write, never the whole buffer again.
	if want := f.writeLens[0] - f.writeLens[0]/2; f.writeLens[1] != want {
		t.Fatalf("retry wrote %d bytes, want only the %d-byte unwritten suffix", f.writeLens[1], want)
	}
	assertSingleValidRecord(t, f.data, raw)
	if successCount != 1 || failCount != 0 {
		t.Fatalf("unexpected DLQ callbacks: success=%d fail=%d", successCount, failCount)
	}
}

func TestWriteQuarantinePartialWriteDoesNotDuplicate(t *testing.T) {
	f := &partialWriteFile{}
	p := &Processor{quarantineFile: f}

	raw := []byte(`{"event":"quarantine"}`)
	p.writeQuarantine(raw, errors.New("schema_validation_failed"))

	if len(f.writeLens) < 2 {
		t.Fatalf("expected a retry after the short write, got %d write calls", len(f.writeLens))
	}
	if want := f.writeLens[0] - f.writeLens[0]/2; f.writeLens[1] != want {
		t.Fatalf("retry wrote %d bytes, want only the %d-byte unwritten suffix", f.writeLens[1], want)
	}
	assertSingleValidRecord(t, f.data, raw)
}

func assertSingleValidRecord(t *testing.T, got []byte, raw []byte) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one JSONL record, got %d lines: %q", len(lines), string(got))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("record is corrupt (partial prefix duplicated?): %v; content: %q", err, string(got))
	}
	if gotRaw, _ := entry["raw"].(string); gotRaw != string(raw) {
		t.Fatalf("raw event mismatch: got %q, want %q", gotRaw, string(raw))
	}
}
