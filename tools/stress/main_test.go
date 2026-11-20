package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"net/http"
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

// When every measured duration is at or above the old one-hour sentinel, the
// reported minimum must be the real minimum, never the sentinel itself.
func TestMinLatencyNotReportedAsSentinel(t *testing.T) {
	const slow = 2 * time.Hour
	tr := runLoadTest("slow", 1, 20*time.Millisecond, func() Result {
		return Result{StatusCode: http.StatusOK, Duration: slow}
	})
	if tr.TotalReqs == 0 {
		t.Fatal("no requests were collected")
	}
	if tr.MinLatency != slow {
		t.Fatalf("MinLatency = %v, want %v (sentinel must not be reported as a real value)", tr.MinLatency, slow)
	}
}

// With no positive durations there is no positive minimum to report; the
// value must stay at zero rather than leaking the one-hour sentinel.
func TestMinLatencyZeroWhenNoPositiveDuration(t *testing.T) {
	tr := runLoadTest("zero", 1, 20*time.Millisecond, func() Result {
		return Result{StatusCode: http.StatusOK, Duration: 0}
	})
	if tr.TotalReqs == 0 {
		t.Fatal("no requests were collected")
	}
	if tr.MinLatency != 0 {
		t.Fatalf("MinLatency = %v, want 0 when all durations are zero", tr.MinLatency)
	}
}

// The smallest positive duration is tracked even when other samples exceed
// the sentinel, preserving the intended "ignore zero durations" behavior.
func TestMinLatencyTracksSmallestPositiveDuration(t *testing.T) {
	const small = 3 * time.Millisecond
	calls := 0
	tr := runLoadTest("mixed", 1, 20*time.Millisecond, func() Result {
		calls++
		if calls%2 == 0 {
			return Result{StatusCode: http.StatusOK, Duration: small}
		}
		return Result{StatusCode: http.StatusOK, Duration: time.Hour + time.Second}
	})
	if tr.MinLatency != small {
		t.Fatalf("MinLatency = %v, want %v", tr.MinLatency, small)
	}
}

// TestNoDeadAtomicRequestCounter guards against reintroducing the write-only
// atomic request counter that was removed from runLoadTest. On the buggy source
// this test fails because main.go imports sync/atomic and calls atomic.AddInt64;
// once the dead counter is removed it passes.
func TestNoDeadAtomicRequestCounter(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	for _, imp := range file.Imports {
		if imp.Path.Value == `"sync/atomic"` {
			t.Errorf("main.go imports sync/atomic: the write-only request counter should be removed")
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkg.Name == "atomic" && sel.Sel.Name == "AddInt64" {
			t.Errorf("main.go calls atomic.AddInt64: the write-only request counter should be removed")
		}
		return true
	})
}

// TestRunLoadTestTotalsFromCollectedResults documents that the authoritative
// request total is derived from the collected results, not from a separate
// side-effect counter. These invariants hold regardless of how many requests
// complete during the test window.
func TestRunLoadTestTotalsFromCollectedResults(t *testing.T) {
	tr := runLoadTest("totals", 2, 100*time.Millisecond, func() Result {
		return Result{StatusCode: 200, Duration: time.Millisecond}
	})

	if tr.TotalReqs != tr.SuccessReqs+tr.ErrorReqs {
		t.Errorf("TotalReqs=%d, want SuccessReqs+ErrorReqs=%d",
			tr.TotalReqs, tr.SuccessReqs+tr.ErrorReqs)
	}

	var byStatus int64
	for _, n := range tr.StatusCodes {
		byStatus += n
	}
	if byStatus != tr.TotalReqs {
		t.Errorf("sum(StatusCodes)=%d, want TotalReqs=%d", byStatus, tr.TotalReqs)
	}
}

func TestErrorRate(t *testing.T) {
	tests := []struct {
		name          string
		errors, total int64
		want          float64
	}{
		{name: "no results collected", errors: 0, total: 0, want: 0},
		{name: "no errors", errors: 0, total: 100, want: 0},
		{name: "half errors", errors: 5, total: 10, want: 0.5},
		{name: "all errors", errors: 7, total: 7, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorRate(tt.errors, tt.total)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("errorRate(%d, %d) = %v; want a finite value", tt.errors, tt.total, got)
			}
			if got != tt.want {
				t.Errorf("errorRate(%d, %d) = %v, want %v", tt.errors, tt.total, got, tt.want)
			}
		})
	}
}

func TestFallbackVersionMatchesCurrentRelease(t *testing.T) {
	if fallbackVersion != "0.4.1" {
		t.Fatalf("fallbackVersion = %q, want current release 0.4.1", fallbackVersion)
	}
}

func TestDefaultOutputPathIsPortable(t *testing.T) {
	if filepath.IsAbs(defaultOutputPath) {
		t.Fatalf("defaultOutputPath %q must be relative", defaultOutputPath)
	}
	if strings.Contains(defaultOutputPath, ":") || strings.HasPrefix(defaultOutputPath, `\\`) {
		t.Fatalf("defaultOutputPath %q must not contain a Windows drive or UNC prefix", defaultOutputPath)
	}
}

func TestWriteResultsJSONCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "reports", "nested", "stress-test-results.json")

	if err := writeResultsJSON(target, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("writeResultsJSON: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("results = %q, want %q", got, `{"ok":true}`)
	}
}
