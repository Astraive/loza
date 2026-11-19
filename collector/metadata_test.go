package lozacollector

import (
	"strings"
	"testing"
)

func TestVersionFromYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "version present",
			yaml:        "version: 1.2.3\nname: loza\n",
			wantVersion: "1.2.3",
		},
		{
			name:        "valid document without version",
			yaml:        "name: loza\nkind: release\n",
			wantVersion: "dev",
		},
		{
			name:        "empty document",
			yaml:        "",
			wantVersion: "dev",
		},
		{
			name:        "malformed yaml is reported, not swallowed",
			yaml:        "version: [unterminated\n",
			wantVersion: "dev",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := versionFromYAML([]byte(tt.yaml))
			if version != tt.wantVersion {
				t.Errorf("version = %q, want %q", version, tt.wantVersion)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "parse loza.yaml metadata") {
				t.Errorf("error %q does not identify the parse failure", err)
			}
		})
	}
}

func TestEmbeddedMetadataParsesCleanly(t *testing.T) {
	t.Parallel()

	if MetadataErr != nil {
		t.Fatalf("embedded loza.yaml failed to parse: %v", MetadataErr)
	}
	if Version == "" || Version == "dev" {
		t.Errorf("Version = %q, want the version declared in loza.yaml", Version)
	}
}
