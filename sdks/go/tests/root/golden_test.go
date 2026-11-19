package loza_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

func TestGoldenTestBaselineAndCompare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "golden.json")

	// A missing golden file must fail the comparison rather than silently
	// passing or being created as a side effect.
	if loza.GoldenTest(path, `{"event":"test","message":"hello"}`) {
		t.Fatal("expected a missing golden file to fail the comparison")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("golden file should not be created by a normal comparison, stat err: %v", err)
	}

	// An explicit update baselines the golden file, creating parent dirs.
	if !loza.GoldenTest(path, `{"event":"test","message":"hello"}`, true) {
		t.Fatal("expected update=true to create the golden file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file was not created: %v", err)
	}
	if want := "{\"event\":\"test\",\"message\":\"hello\"}\n"; string(data) != want {
		t.Fatalf("unexpected golden content: got %q, want %q", data, want)
	}

	// Once baselined, a matching snapshot passes and a differing one fails.
	if !loza.GoldenTest(path, `{"event":"test","message":"hello"}`) {
		t.Fatal("expected a matching snapshot to pass")
	}
	if loza.GoldenTest(path, `{"event":"test","message":"changed"}`) {
		t.Fatal("expected a differing snapshot to fail")
	}
}

func TestGoldenTestCanonicalComparison(t *testing.T) {
	path := filepath.Join(t.TempDir(), "golden.json")
	if !loza.GoldenTest(path, `{"b":2,"a":1}`, true) {
		t.Fatal("expected update=true to succeed")
	}

	// Object-key ordering and insignificant whitespace must not cause a
	// false mismatch.
	if !loza.GoldenTest(path, "{ \"a\": 1, \"b\": 2 }") {
		t.Fatal("expected canonical JSON comparison to ignore key order and whitespace")
	}
}
