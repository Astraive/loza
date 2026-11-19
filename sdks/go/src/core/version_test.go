package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVersionFromYAMLTopLevelOnly verifies that only an unindented, top-level
// "version:" key is used. Indented "version:" keys from nested sections (such
// as a dependency's version) must not win, even when they appear first.
func TestVersionFromYAMLTopLevelOnly(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "top-level version with no nested keys",
			yaml: "name: loza-go\nversion: 0.4.1\nlicense: MIT\n",
			want: "0.4.1",
		},
		{
			name: "nested version before top-level version is ignored",
			yaml: "name: loza-go\ndependencies:\n  some-dep:\n    version: 9.9.9\nversion: 0.4.1\n",
			want: "0.4.1",
		},
		{
			name: "nested version after top-level version is ignored",
			yaml: "version: 0.4.1\ndependencies:\n  some-dep:\n    version: 9.9.9\n",
			want: "0.4.1",
		},
		{
			name: "deeply indented nested version is ignored",
			yaml: "metadata:\n    dependencies:\n        foo:\n            version: 7.7.7\nversion: 0.4.2\n",
			want: "0.4.2",
		},
		{
			name: "only nested version present returns empty",
			yaml: "dependencies:\n  some-dep:\n    version: 9.9.9\n",
			want: "",
		},
		{
			name: "quoted top-level version is unquoted",
			yaml: "version: \"0.4.3\"\ndependencies:\n  foo:\n    version: 9.9.9\n",
			want: "0.4.3",
		},
		{
			name: "commented out nested version does not match",
			yaml: "  # version: 9.9.9\nversion: 0.4.4\n",
			want: "0.4.4",
		},
		{
			name: "no version key returns empty",
			yaml: "name: loza-go\nlicense: MIT\n",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionFromYAML([]byte(tt.yaml)); got != tt.want {
				t.Errorf("versionFromYAML() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoadVersionIgnoresNestedVersion exercises the full file-reading path to
// confirm a nested "version:" in loza-go.yaml does not override the top-level
// value. This is the regression reported in issue #688.
func TestLoadVersionIgnoresNestedVersion(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatal(err)
		}
	})

	dir := t.TempDir()
	yaml := "name: loza-go\ndependencies:\n  some-dep:\n    version: 9.9.9\nversion: 0.4.1\n"
	if err := os.WriteFile(filepath.Join(dir, "loza-go.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if got := loadVersion(); got != "0.4.1" {
		t.Errorf("loadVersion() = %q, want %q", got, "0.4.1")
	}
}

// TestLoadVersionFallsBackWhenOnlyNestedVersion verifies that a file with a
// nested "version:" but no top-level one falls back to the hardcoded default.
func TestLoadVersionFallsBackWhenOnlyNestedVersion(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatal(err)
		}
	})

	dir := t.TempDir()
	yaml := "name: loza-go\ndependencies:\n  some-dep:\n    version: 9.9.9\n"
	if err := os.WriteFile(filepath.Join(dir, "loza-go.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if got := loadVersion(); got != fallbackVersion {
		t.Errorf("loadVersion() = %q, want fallback %q", got, fallbackVersion)
	}
}
