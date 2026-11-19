package version

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

const fallbackVersion = "0.4.1"

var (
	once    sync.Once
	version string
)

// CollectorVersion returns the collector version from loza.yaml,
// falling back to the hardcoded default if the file cannot be found or parsed.
func CollectorVersion() string {
	once.Do(func() {
		version = loadVersion()
	})
	return version
}

func loadVersion() string {
	candidates := []string{
		"loza.yaml",
		"../loza.yaml",
		"../../loza.yaml",
	}
	return versionFromFiles(candidates)
}

// versionFromFiles returns the top-level version from the first candidate file
// that contains one, or fallbackVersion if none of the files do.
func versionFromFiles(paths []string) string {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if v := versionFromYAML(data); v != "" {
			return v
		}
	}
	return fallbackVersion
}

// versionFromYAML parses data as YAML and returns only the top-level "version"
// field. Unmarshalling into a struct ensures nested "version" keys are ignored.
func versionFromYAML(data []byte) string {
	var doc struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.Version
}
