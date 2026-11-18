package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
