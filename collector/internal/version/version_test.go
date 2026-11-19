package version

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionFromYAMLTopLevelOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "top-level version before nested version",
			data: "version: \"1.2.3\"\ncomponent:\n  version: \"9.9.9\"\n",
			want: "1.2.3",
		},
		{
			name: "nested version before top-level version",
			data: "component:\n  version: \"9.9.9\"\nversion: \"1.2.3\"\n",
			want: "1.2.3",
		},
		{
			name: "deeply nested version before top-level version",
			data: "outer:\n  inner:\n    version: 8.8.8\nversion: 1.2.3\n",
			want: "1.2.3",
		},
		{
			name: "only nested version is ignored",
			data: "component:\n  version: \"9.9.9\"\n",
			want: "",
		},
		{
			name: "no version key",
			data: "name: loza\nkind: release\n",
			want: "",
		},
		{
			name: "quoted top-level version",
			data: "name: loza\nversion: '0.4.1'\ndescription: test\n",
			want: "0.4.1",
		},
		{
			name: "invalid yaml",
			data: "version: [unclosed\n",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := versionFromYAML([]byte(tt.data)); got != tt.want {
				t.Errorf("versionFromYAML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionFromFilesSkipsFilesWithoutTopLevelVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The first candidate has only a nested version: it must be skipped so
	// that a later candidate with a real top-level version is used instead.
	first := filepath.Join(dir, "first.yaml")
	if err := os.WriteFile(first, []byte("component:\n  version: 9.9.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second.yaml")
	if err := os.WriteFile(second, []byte("name: loza\nversion: 1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := versionFromFiles([]string{first, second}); got != "1.2.3" {
		t.Errorf("versionFromFiles() = %q, want %q", got, "1.2.3")
	}
}

func TestVersionFromFilesFallsBackWhenOnlyNestedVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "loza.yaml")
	if err := os.WriteFile(path, []byte("component:\n  version: 9.9.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := versionFromFiles([]string{path}); got != fallbackVersion {
		t.Errorf("versionFromFiles() = %q, want fallback %q", got, fallbackVersion)
	}
}
// TestLoadVersionStripsInlineComment verifies that an inline YAML comment on
// the version line is not returned as part of the parsed version. This is a
// regression test for the defect where `version: 0.4.2 # comment` was returned
// verbatim (and then cached by CollectorVersion via sync.Once).
func TestLoadVersionStripsInlineComment(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "inline comment after unquoted version",
			yaml: "name: loza\nversion: 0.4.2 # comment\nlicense: MIT\n",
			want: "0.4.2",
		},
		{
			name: "inline comment after double quoted version",
			yaml: "version: \"0.4.2\" # release version\n",
			want: "0.4.2",
		},
		{
			name: "inline comment after single quoted version",
			yaml: "version: '0.4.2' # release version\n",
			want: "0.4.2",
		},
		{
			name: "hash inside quoted value is not a comment",
			yaml: "version: '0.4.2#build' # trailing comment\n",
			want: "0.4.1", // fails validation, falls back
		},
		{
			name: "plain version without comment",
			yaml: "version: 0.4.2\n",
			want: "0.4.2",
		},
		{
			name: "commented out version line is ignored",
			yaml: "# version: 9.9.9\nname: loza\n",
			want: "0.4.1",
		},
		{
			name: "version line that is only a comment falls back",
			yaml: "version: # no version here\n",
			want: "0.4.1",
		},
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatal(err)
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "loza.yaml"), []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			if got := loadVersion(); got != tt.want {
				t.Errorf("loadVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
