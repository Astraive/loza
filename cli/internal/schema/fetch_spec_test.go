package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureSpecAssetsValidatesLocally locks in the real behavior behind the
// accurately named EnsureSpecAssets function: it validates local spec assets
// and never performs a network fetch.
func TestEnsureSpecAssetsValidatesLocally(t *testing.T) {
	t.Run("reports missing assets", func(t *testing.T) {
		err := EnsureSpecAssets(t.TempDir())
		if err == nil {
			t.Fatal("expected an error for a spec repo with no required assets")
		}
		if !strings.Contains(err.Error(), "missing spec asset") {
			t.Fatalf("expected a missing spec asset error, got %v", err)
		}
	})

	t.Run("accepts a complete local spec repo", func(t *testing.T) {
		if err := EnsureSpecAssets(specRepoRoot(t)); err != nil {
			t.Fatalf("EnsureSpecAssets failed for a complete local spec repo: %v", err)
		}
	})
}

// TestEnsureSpecAssetsRejectsPartialRepo guards against a future change that
// silently treats a partially populated spec repo as valid.
func TestEnsureSpecAssetsRejectsPartialRepo(t *testing.T) {
	root := specRepoRoot(t)
	partial := t.TempDir()

	// Mirror just one required asset into an otherwise empty repo.
	source := EventSchemaPath(root)
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("expected event schema at %s: %v", source, err)
	}
	dest := filepath.Join(partial, "spec", "schemas", "json", "event.schema.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read event schema: %v", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatalf("write event schema: %v", err)
	}

	if err := EnsureSpecAssets(partial); err == nil {
		t.Fatal("expected an error for a spec repo missing all but one required asset")
	}
}
