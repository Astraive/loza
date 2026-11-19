// Package lozacortex provides build-time metadata for the loza-cortex module.
package lozacortex

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed loza-cortex.yaml
var metadataYAML []byte

// Version is the module version read from loza-cortex.yaml at build time.
var Version = loadVersion()

func loadVersion() string {
	return versionFromYAML(metadataYAML)
}

// versionFromYAML extracts the module version from the embedded metadata.
// Because the metadata is compiled into the binary, a parse error or a missing
// version is a build-time misconfiguration and must be surfaced loudly rather
// than silently falling back to "dev".
func versionFromYAML(data []byte) string {
	var m struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		panic(fmt.Sprintf("lozacortex: invalid embedded metadata YAML: %v", err))
	}
	if m.Version == "" {
		panic("lozacortex: embedded metadata YAML is missing a non-empty 'version' field")
	}
	return m.Version
}
