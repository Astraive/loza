package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
	"gopkg.in/yaml.v3"
)

func validInitConfig() config.Config {
	return config.Config{
		CollectorRepoPath: "/collector",
		SpecRepoPath:      "/spec",
		CollectorURL:      "http://127.0.0.1:9308",
	}
}

// TestInitCommandRejectsInvalidValidationMode reproduces the reported defect:
// an invalid mode used to be written to the generated config, which the CLI
// later refused to load. It must now fail before any file is written.
func TestInitCommandRejectsInvalidValidationMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.yaml")

	err := InitCommand(validInitConfig(), []string{"--validation-mode", "bogus", target})
	if err == nil {
		t.Fatal("expected an error for an invalid validation mode")
	}
	if !strings.Contains(err.Error(), "validation_mode") {
		t.Fatalf("expected validation_mode error, got %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected no config file to be written, got stat error %v", statErr)
	}
}

// TestInitCommandWritesValidValidationMode verifies that supported modes are
// normalized and persisted in a config that passes the same validation the
// loader applies.
func TestInitCommandWritesValidValidationMode(t *testing.T) {
	cases := []struct {
		flag string
		want string
	}{
		{"off", "off"},
		{"warn", "warn"},
		{"enforce", "enforce"},
		{"quarantine", "quarantine"},
		{"WARN", "warn"},
		{" quarantine ", "quarantine"},
	}

	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "config.yaml")
			if err := InitCommand(validInitConfig(), []string{"--validation-mode", tc.flag, target}); err != nil {
				t.Fatalf("init with mode %q: %v", tc.flag, err)
			}

			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read generated config: %v", err)
			}
			var got config.Config
			if err := yaml.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal generated config: %v", err)
			}
			if got.ValidationMode != tc.want {
				t.Fatalf("validation_mode = %q, want %q", got.ValidationMode, tc.want)
			}
			if err := config.Validate(got); err != nil {
				t.Fatalf("generated config fails validation: %v", err)
			}
		})
	}
}

func TestInitCommandRefusesToOverwriteExistingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".loza-cli.yaml")
	sentinel := "cortex:\n  api_key: super-secret-token\n"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	_, err := captureStdout(t, func() error {
		return InitCommand(config.Config{}, []string{target})
	})
	if err == nil {
		t.Fatalf("expected an error when target already exists")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected error to mention --force, got %v", err)
	}

	raw, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(raw) != sentinel {
		t.Fatalf("target was modified without --force: got %q", string(raw))
	}
}

func TestInitCommandForceOverwritesExistingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".loza-cli.yaml")
	if err := os.WriteFile(target, []byte("old contents\n"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	_, err := captureStdout(t, func() error {
		return InitCommand(config.Config{CollectorURL: "http://127.0.0.1:9308"}, []string{"--force", target})
	})
	if err != nil {
		t.Fatalf("init with --force: %v", err)
	}

	raw, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if !strings.Contains(string(raw), "collector_url: http://127.0.0.1:9308") {
		t.Fatalf("target was not regenerated with --force: got %q", string(raw))
	}
}

func TestInitCommandCreatesNewFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".loza-cli.yaml")

	_, err := captureStdout(t, func() error {
		return InitCommand(config.Config{CollectorURL: "http://127.0.0.1:9308"}, []string{target})
	})
	if err != nil {
		t.Fatalf("init new target: %v", err)
	}

	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected new target file: %v", statErr)
	}
}
