package schema

import (
	"os"
	"path/filepath"
)

func firstExistingPath(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return paths[0]
}

func EventSchemaPath(specRepoPath string) string {
	return firstExistingPath(
		filepath.Join(specRepoPath, "spec", "schemas", "json", "event.schema.json"),
		filepath.Join(specRepoPath, "schema", "event.schema.json"),
	)
}

// EnsureSpecAssets verifies that a local spec repository contains all of the
// assets required by the CLI. It performs only local filesystem checks and does
// not fetch or clone anything from the network.
func EnsureSpecAssets(specRepoPath string) error {
	return ValidateSpecAssets(specRepoPath)
}
