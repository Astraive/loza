// Package lozacollector provides build-time metadata for the loza-collector module.
package lozacollector

import (
	_ "embed"
	"fmt"
	"log"

	"gopkg.in/yaml.v3"
)

//go:embed loza.yaml
var metadataYAML []byte

var (
	// MetadataErr reports the error, if any, encountered while parsing the
	// embedded loza.yaml at init time. A nil value means the metadata parsed
	// successfully; Version may still be "dev" if the document is valid but
	// does not declare a version.
	MetadataErr error

	// Version is the module version read from loza.yaml at build time.
	// It is "dev" when the metadata is valid but omits a version, or when the
	// embedded metadata cannot be parsed. Use MetadataErr to tell those two
	// cases apart.
	Version = loadVersion()
)

func loadVersion() string {
	version, err := versionFromYAML(metadataYAML)
	if err != nil {
		MetadataErr = err
		log.Printf("lozacollector: failed to parse embedded loza.yaml metadata: %v", err)
	}
	return version
}

// versionFromYAML parses the version from the supplied metadata YAML. It
// returns "dev" when the document is valid but does not declare a version, and
// a non-nil error when the document cannot be parsed.
func versionFromYAML(data []byte) (string, error) {
	var m struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return "dev", fmt.Errorf("parse loza.yaml metadata: %w", err)
	}
	if m.Version == "" {
		return "dev", nil
	}
	return m.Version, nil
}
