package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/commands"
	"github.com/astraive/loza/cli/internal/config"
)

func TestSplitGlobalFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantArgs    []string
		wantVerbose bool
		wantFormat  string
	}{
		{
			name:       "emit sample output path is preserved",
			args:       []string{"emit", "sample", "--output", "/tmp/x.json", "--print"},
			wantArgs:   []string{"emit", "sample", "--output", "/tmp/x.json", "--print"},
			wantFormat: "text",
		},
		{
			name:       "schema fetch output path is preserved",
			args:       []string{"schema", "fetch", "--output", "schema.json"},
			wantArgs:   []string{"schema", "fetch", "--output", "schema.json"},
			wantFormat: "text",
		},
		{
			name:       "export output path is preserved",
			args:       []string{"export", "--output", "events.json", "--format", "json"},
			wantArgs:   []string{"export", "--output", "events.json", "--format", "json"},
			wantFormat: "text",
		},
		{
			name:       "dangling output flag is preserved",
			args:       []string{"emit", "sample", "--output"},
			wantArgs:   []string{"emit", "sample", "--output"},
			wantFormat: "text",
		},
		{
			name:       "global format after command is still consumed",
			args:       []string{"status", "--output", "json"},
			wantArgs:   []string{"status"},
			wantFormat: "json",
		},
		{
			name:       "global format before command is consumed",
			args:       []string{"--output", "table", "query", "-q", "SELECT 1"},
			wantArgs:   []string{"query", "-q", "SELECT 1"},
			wantFormat: "table",
		},
		{
			name:       "output equals format is still consumed",
			args:       []string{"--output=json", "status"},
			wantArgs:   []string{"status"},
			wantFormat: "json",
		},
		{
			name:        "verbose is consumed anywhere",
			args:        []string{"--verbose", "status", "--output", "text"},
			wantArgs:    []string{"status"},
			wantVerbose: true,
			wantFormat:  "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, gotVerbose, gotFormat := splitGlobalFlags(tt.args)
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("remaining args = %q, want %q", gotArgs, tt.wantArgs)
			}
			if gotVerbose != tt.wantVerbose {
				t.Fatalf("verbose = %v, want %v", gotVerbose, tt.wantVerbose)
			}
			if gotFormat != tt.wantFormat {
				t.Fatalf("format = %q, want %q", gotFormat, tt.wantFormat)
			}
		})
	}
}

// TestEmitOutputPathSurvivesGlobalFlagParsing reproduces the reported bug:
// through the real CLI the global parser removes the --output file-path
// argument before EmitCommand can see it.
func TestEmitOutputPathSurvivesGlobalFlagParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.json")
	args, _, _ := splitGlobalFlags([]string{"emit", "sample", "--output", path, "--print"})
	if !reflect.DeepEqual(args, []string{"emit", "sample", "--output", path, "--print"}) {
		t.Fatalf("global parser altered args: %q", args)
	}

	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	if err := commands.EmitCommand(cfg, args[1:]); err != nil {
		t.Fatalf("emit sample: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sample output file was not written: %v", err)
	}
	if !strings.Contains(string(raw), `"api_version": "v1"`) {
		t.Fatalf("unexpected sample output: %s", raw)
	}
}
