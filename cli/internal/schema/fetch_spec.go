package schema

import "path/filepath"

// firstExistingPath delegates to firstExisting so both call sites share the same
// guarded behaviour: no panic on empty input, and non-IsNotExist stat errors are
// not mistaken for a missing path.
func firstExistingPath(paths ...string) string {
	return firstExisting(paths...)
}

func EventSchemaPath(specRepoPath string) string {
	return firstExistingPath(
		filepath.Join(specRepoPath, "spec", "schemas", "json", "event.schema.json"),
		filepath.Join(specRepoPath, "schema", "event.schema.json"),
	)
}

func FetchSpec(specRepoPath string) error {
	return ValidateSpecAssets(specRepoPath)
}
