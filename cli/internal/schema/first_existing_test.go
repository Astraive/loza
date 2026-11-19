package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstExistingWithNoPaths(t *testing.T) {
	if got := firstExisting(); got != "" {
		t.Fatalf("firstExisting() = %q, want empty string", got)
	}
	if got := firstExistingPath(); got != "" {
		t.Fatalf("firstExistingPath() = %q, want empty string", got)
	}
}

func TestFirstExistingSelectionAndFallback(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	if got := firstExisting(first, second); got != first {
		t.Fatalf("firstExisting() = %q, want %q", got, first)
	}
	if got := firstExistingPath(first, second); got != first {
		t.Fatalf("firstExistingPath() = %q, want %q", got, first)
	}

	missing := filepath.Join(root, "missing")
	if got := firstExisting(missing, second); got != second {
		t.Fatalf("firstExisting() = %q, want %q", got, second)
	}

	if got := firstExisting(missing, filepath.Join(root, "also-missing")); got != missing {
		t.Fatalf("firstExisting() fallback = %q, want %q", got, missing)
	}
}

func TestFirstExistingKeepsNonNotExistErrorCandidate(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Skipf("cannot create symlink loop: %v", err)
	}
	if _, err := os.Stat(loop); os.IsNotExist(err) || err == nil {
		t.Skipf("os.Stat on a symlink loop did not yield a non-IsNotExist error: %v", err)
	}

	present := filepath.Join(root, "present")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// A path that exists but cannot be stat-ed normally must not be discarded in
	// favour of a later path: only os.IsNotExist(err) means "missing".
	if got := firstExisting(loop, present); got != loop {
		t.Fatalf("firstExisting() = %q, want %q for non-IsNotExist stat error", got, loop)
	}
	if got := firstExistingPath(loop, present); got != loop {
		t.Fatalf("firstExistingPath() = %q, want %q for non-IsNotExist stat error", got, loop)
	}
}
