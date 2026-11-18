package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
)

// captureStderrForTest redirects os.Stderr for the duration of run and returns
// everything written to it. Warnings emitted by the NDJSON parser are small, so a
// single pipe read after closing the writer is sufficient.
func captureStderrForTest(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	run()
	_ = w.Close()
	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	_ = r.Close()
	return string(buf[:n])
}

func writeNDJSONFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.ndjson")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write NDJSON file: %v", err)
	}
	return path
}

// A malformed line must not be silently discarded: the valid events are still
// returned, and stderr reports how many lines were skipped and which ones.
func TestParseNDJSONFileReportsMalformedLines(t *testing.T) {
	path := writeNDJSONFile(t,
		`{"id":"ok-1"}`,
		`{not valid json}`,
		``,
		`{"id":"ok-2"}`,
		`{"id":`,
	)

	var events []map[string]interface{}
	var err error
	stderr := captureStderrForTest(t, func() {
		events, err = parseNDJSONFile(path)
	})
	if err != nil {
		t.Fatalf("parseNDJSONFile: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0]["id"] != "ok-1" || events[1]["id"] != "ok-2" {
		t.Fatalf("unexpected parsed events: %v", events)
	}
	for _, want := range []string{"skipped 2 unparseable line(s)", "lines: 2, 5", path} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q does not contain %q", stderr, want)
		}
	}
}

// bufio.Scanner's default 64 KiB token limit used to stop scanning entirely on a
// larger event. Such a line must now be ingested, and scanning must continue.
func TestParseNDJSONFileAcceptsLargeLines(t *testing.T) {
	largeValue := strings.Repeat("a", 128*1024)
	path := writeNDJSONFile(t,
		`{"id":"large","blob":"`+largeValue+`"}`,
		`{"id":"after-large"}`,
	)

	events, err := parseNDJSONFile(path)
	if err != nil {
		t.Fatalf("parseNDJSONFile: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0]["id"] != "large" || events[1]["id"] != "after-large" {
		t.Fatalf("unexpected parsed events: %#v", events)
	}
}

// An over-limit line must surface as an error instead of silently truncating
// ingestion, even when valid lines were parsed before it.
func TestParseNDJSONFileLineTooLongReturnsError(t *testing.T) {
	path := writeNDJSONFile(t,
		`{"id":"before"}`,
		`{"blob":"`+strings.Repeat("a", maxNDJSONLineSize)+`"}`,
	)

	_, err := parseNDJSONFile(path)
	if err == nil {
		t.Fatal("expected an error for an over-limit line, got nil")
	}
	if !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("expected scanner token-too-long error, got %v", err)
	}
}

func TestParseNDJSONFileOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.ndjson")
	if _, err := parseNDJSONFile(path); err == nil {
		t.Fatal("expected an error opening a missing file, got nil")
	}
}

// The warning keeps the full count but bounds the number of listed line numbers.
func TestNDJSONSkippedWarningIsBounded(t *testing.T) {
	lines := make([]int, 25)
	for i := range lines {
		lines[i] = i + 1
	}
	got := ndjsonSkippedWarning("events.ndjson", lines)
	if !strings.Contains(got, "skipped 25 unparseable line(s)") {
		t.Fatalf("warning %q does not report the full count", got)
	}
	if !strings.HasSuffix(got, "lines: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, ...)") {
		t.Fatalf("warning %q does not bound the listed line numbers", got)
	}
}

// RunCortexIngest must propagate a parse failure rather than reporting a
// misleadingly low ingested count.
func TestRunCortexIngestPropagatesParseError(t *testing.T) {
	path := writeNDJSONFile(t, `{"blob":"`+strings.Repeat("a", maxNDJSONLineSize)+`"}`)
	err := RunCortexIngest(context.Background(), config.Config{}, []string{"--file", path})
	if err == nil {
		t.Fatal("expected RunCortexIngest to propagate the parse error, got nil")
	}
	if !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("expected wrapped scanner error, got %v", err)
	}
}
