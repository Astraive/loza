package schema

import "path/filepath"

// firstExistingPath delegates to firstExisting so both call sites share the same
// guarded behaviour: no panic on empty input, and non-IsNotExist stat errors are
// not mistaken for a missing path.
func firstExistingPath(paths ...string) string {
	return firstExisting(paths...)
}

func EventSchemaPath(specRepoPath string) string {
	return firstExisting(
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
