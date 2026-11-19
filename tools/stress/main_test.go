package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultJSONOutputPathIsRelative(t *testing.T) {
	if filepath.IsAbs(defaultJSONOutputPath) {
		t.Fatalf("defaultJSONOutputPath = %q, want a relative path", defaultJSONOutputPath)
	}
	if strings.Contains(defaultJSONOutputPath, `\`) || strings.Contains(defaultJSONOutputPath, ":") {
		t.Fatalf("defaultJSONOutputPath = %q, want a portable path without Windows drive/separators", defaultJSONOutputPath)
	}
	if want := filepath.Join("docs", "stress-test-results.json"); defaultJSONOutputPath != want {
		t.Fatalf("defaultJSONOutputPath = %q, want %q", defaultJSONOutputPath, want)
	}
}

func TestWriteAndReportJSONResultsSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stress-test-results.json")
	results := map[string]any{
		"target":       "localhost",
		"total_reqs":   int64(42),
		"total_errors": int64(0),
		"tests":        []map[string]any{{"name": "health"}},
	}

	var out bytes.Buffer
	if ok := writeAndReportJSONResults(&out, path, results); !ok {
		t.Fatalf("writeAndReportJSONResults() = false, want true; output: %s", out.String())
	}
	if strings.Contains(out.String(), "ERROR") {
		t.Errorf("output contains an error on success: %s", out.String())
	}
	if want := "JSON results written to " + path; !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not contain %q", out.String(), want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("written results are not valid JSON: %v\n%s", err, data)
	}
	if got["target"] != "localhost" {
		t.Errorf("target = %v, want localhost", got["target"])
	}
}

// Regression test for the reported defect: a failed write must not be
// reported as a success (and the error must no longer be silently ignored).
func TestWriteAndReportJSONResultsWriteFailureDoesNotReportSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "stress-test-results.json")

	var out bytes.Buffer
	if ok := writeAndReportJSONResults(&out, path, map[string]any{"target": "localhost"}); ok {
		t.Fatal("writeAndReportJSONResults() = true for an unwritable path, want false")
	}
	if strings.Contains(out.String(), "JSON results written to") {
		t.Errorf("failed write reported success: %s", out.String())
	}
	if want := "ERROR: failed to write JSON results to " + path; !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not report the write error %q", out.String(), want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file unexpectedly exists at %q (stat err: %v)", path, err)
	}
}

func TestWriteAndReportJSONResultsMarshalFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stress-test-results.json")

	var out bytes.Buffer
	results := map[string]any{"unsupported": func() {}}
	if ok := writeAndReportJSONResults(&out, path, results); ok {
		t.Fatal("writeAndReportJSONResults() = true for unmarshalable data, want false")
	}
	if strings.Contains(out.String(), "JSON results written to") {
		t.Errorf("failed encode reported success: %s", out.String())
	}
	if want := "ERROR: failed to encode JSON results"; !strings.Contains(out.String(), want) {
		t.Errorf("output %q does not report the encode error %q", out.String(), want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file unexpectedly exists at %q (stat err: %v)", path, err)
	}
}

// TestSubmitResultBlocksInsteadOfDropping reproduces the reported defect: with
// a full results buffer the old non-blocking `select { case results <- r:
// default: }` discarded the measurement. submitResult must instead block until
// the collector has room, so no result is ever silently lost.
func TestSubmitResultBlocksInsteadOfDropping(t *testing.T) {
	results := make(chan Result, 1)
	results <- Result{StatusCode: 200, Duration: time.Millisecond} // fill the buffer

	returned := make(chan struct{})
	go func() {
		submitResult(results, Result{StatusCode: 201, Duration: 2 * time.Millisecond})
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("submitResult returned while the buffer was full; the result was dropped")
	case <-time.After(50 * time.Millisecond):
		// Expected: the blocking send is waiting for the collector.
	}

	// Free one slot; the pending result must be delivered, not lost.
	if got := <-results; got.StatusCode != 200 {
		t.Fatalf("first result status = %d, want 200", got.StatusCode)
	}
	select {
	case got := <-results:
		if got.StatusCode != 201 {
			t.Fatalf("deferred result status = %d, want 201", got.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("deferred result was never delivered to the collector")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("submitResult did not return after the result was consumed")
	}
}

// TestRunLoadTestNoDroppedResults checks the end-to-end invariant: every
// request made must be reflected in the statistics, with an explicit zero
// dropped count under sustained load.
func TestRunLoadTestNoDroppedResults(t *testing.T) {
	var generated int64
	tr := runLoadTest("no-drop", 4, 60*time.Millisecond, func() Result {
		atomic.AddInt64(&generated, 1)
		return Result{StatusCode: 200, Duration: time.Microsecond}
	})

	got := atomic.LoadInt64(&generated)
	if got == 0 {
		t.Fatal("reqFunc was never called; test is inconclusive")
	}
	if tr.TotalReqs != got {
		t.Fatalf("TotalReqs = %d, want %d (every request result must be counted)", tr.TotalReqs, got)
	}
	if tr.DroppedReqs != 0 {
		t.Fatalf("DroppedReqs = %d, want 0", tr.DroppedReqs)
	}
	if tr.SuccessReqs != got {
		t.Fatalf("SuccessReqs = %d, want %d", tr.SuccessReqs, got)
	}
}
