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

// TestEventSchemaPathUsesSharedHelper guards against reintroducing a separate
// first-existing implementation in fetch_spec.go: EventSchemaPath must resolve
// paths with the same shared firstExisting helper used by ValidateSpecAssets.
func TestEventSchemaPathUsesSharedHelper(t *testing.T) {
	t.Run("prefers spec layout when it exists", func(t *testing.T) {
		root := t.TempDir()
		primary := filepath.Join(root, "spec", "schemas", "json", "event.schema.json")
		writeFile(t, primary)

		// Both layouts exist; the shared helper must select the first existing one.
		writeFile(t, filepath.Join(root, "schema", "event.schema.json"))
		if got := EventSchemaPath(root); got != primary {
			t.Fatalf("EventSchemaPath = %q, want %q", got, primary)
		}
	})

	t.Run("falls back to alternate layout", func(t *testing.T) {
		root := t.TempDir()
		alternate := filepath.Join(root, "schema", "event.schema.json")
		writeFile(t, alternate)

		if got := EventSchemaPath(root); got != alternate {
			t.Fatalf("EventSchemaPath = %q, want %q", got, alternate)
		}
	})

	t.Run("returns first path when none exist", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, "spec", "schemas", "json", "event.schema.json")
		if got := EventSchemaPath(root); got != want {
			t.Fatalf("EventSchemaPath = %q, want %q", got, want)
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

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
