package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkerConfigFromArgsReportsBrokenDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-defaults.yaml")
	t.Setenv("LOZA_COLLECTOR_DEFAULTS", missing)

	_, err := loadWorkerConfigFromArgs(nil)
	if err == nil {
		t.Fatal("expected an error for a missing bundled defaults file")
	}
	if !strings.Contains(err.Error(), "collector defaults") {
		t.Fatalf("expected a descriptive defaults error, got: %v", err)
	}
}
