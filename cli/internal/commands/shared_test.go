package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileBytesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file permissions are not meaningful on Windows")
	}

	path := filepath.Join(t.TempDir(), "export.ndjson")
	if err := writeFileBytes(path, []byte("sensitive event data\n")); err != nil {
		t.Fatalf("writeFileBytes: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat export file: %v", err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Fatalf("export file permissions = %o, want no group/world access", perm)
	}
	if perm != 0o600 {
		t.Fatalf("export file permissions = %o, want 600", perm)
	}
}
