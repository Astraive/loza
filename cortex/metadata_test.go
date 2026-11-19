package lozacortex

import (
	"strings"
	"testing"
)

func TestLoadVersionFromEmbeddedMetadata(t *testing.T) {
	// The embedded metadata is shipped with the build and must resolve to the
	// real module version rather than the old silent "dev" fallback.
	if Version == "" || Version == "dev" {
		t.Fatalf("Version = %q, want the real embedded version", Version)
	}
}

func TestVersionFromYAML(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		want      string
		wantPanic string
	}{
		{
			name: "valid version",
			data: "name: loza-cortex\nversion: 1.2.3\n",
			want: "1.2.3",
		},
		{
			name:      "malformed yaml",
			data:      "version: [unterminated\n",
			wantPanic: "invalid embedded metadata YAML",
		},
		{
			name:      "missing version key",
			data:      "name: loza-cortex\nkind: docker\n",
			wantPanic: "missing a non-empty 'version' field",
		},
		{
			name:      "misspelled version key",
			data:      "versoin: 1.2.3\n",
			wantPanic: "missing a non-empty 'version' field",
		},
		{
			name:      "empty version value",
			data:      "version: \"\"\n",
			wantPanic: "missing a non-empty 'version' field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, panicVal := captureVersionFromYAML(tt.data)

			if tt.wantPanic != "" {
				if panicVal == nil {
					t.Fatalf("versionFromYAML() = %q, want panic containing %q", got, tt.wantPanic)
				}
				if msg, ok := panicVal.(string); !ok || !strings.Contains(msg, tt.wantPanic) {
					t.Fatalf("panic = %v, want message containing %q", panicVal, tt.wantPanic)
				}
				return
			}

			if panicVal != nil {
				t.Fatalf("versionFromYAML() panicked: %v", panicVal)
			}
			if got != tt.want {
				t.Fatalf("versionFromYAML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func captureVersionFromYAML(data string) (v string, panicVal any) {
	defer func() { panicVal = recover() }()
	return versionFromYAML([]byte(data)), nil
}
