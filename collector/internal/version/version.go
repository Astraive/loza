package version

import (
	"os"
	"strings"
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
// The parsed value is still normalized and validated so inline comment
// remnants or otherwise invalid tokens cannot leak into the reported version.
func versionFromYAML(data []byte) string {
	var doc struct {
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	value := stripInlineComment(doc.Version)
	value = strings.Trim(value, "\"'")
	if !isValidVersion(value) {
		return ""
	}
	return value
}

// stripInlineComment removes a YAML inline comment from value. In YAML a '#'
// starts a comment only when it is at the start of a scalar or is preceded by
// whitespace; a '#' inside a quoted scalar is part of the value.
func stripInlineComment(value string) string {
	var quote rune
	for i, r := range value {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#':
			if i == 0 || value[i-1] == ' ' || value[i-1] == '\t' {
				return strings.TrimSpace(value[:i])
			}
		}
	}
	return strings.TrimSpace(value)
}

// isValidVersion reports whether s is a plausible version token. It rejects
// empty values and anything containing comment remnants or whitespace while
// accepting the numeric/semver-style versions used across the repository.
func isValidVersion(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r == '.', r == '-', r == '+', r == '_':
			continue
		default:
			return false
		}
	}
	return true
}
