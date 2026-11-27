package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSinkReferenceExamplesLoadThroughStrictConfigLoader(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	docPath := filepath.Join(repoRoot, "collector", "docs", "sink-reference.md")
	markdown, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read sink reference: %v", err)
	}
	document := strings.ReplaceAll(string(markdown), "\r\n", "\n")

	var examples []string
	var current []string
	inYAML := false
	for _, line := range strings.Split(document, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inYAML && trimmed == "```yaml" {
			inYAML = true
			current = nil
			continue
		}
		if inYAML && trimmed == "```" {
			examples = append(examples, strings.Join(current, "\n"))
			inYAML = false
			current = nil
			continue
		}
		if inYAML {
			current = append(current, line)
		}
	}
	if inYAML {
		t.Fatal("sink reference has an unclosed YAML code block")
	}
	if len(examples) == 0 {
		t.Fatal("sink reference has no YAML examples")
	}

	for i, example := range examples {
		var cfg Config
		if err := Load([]byte(example), &cfg); err != nil {
			t.Errorf("YAML example %d does not load: %v", i+1, err)
			continue
		}
		if len(cfg.Fanout.Outputs) != 1 {
			t.Errorf("YAML example %d has %d fanout outputs, want 1", i+1, len(cfg.Fanout.Outputs))
			continue
		}

		output := cfg.Fanout.Outputs[0]
		if output.Name == "" || output.Type == "" || !output.Enabled {
			t.Errorf("YAML example %d is missing enabled fanout output metadata: %+v", i+1, output)
		}
		nestedConfigSet := false
		switch output.Type {
		case "duckdb":
			nestedConfigSet = output.DuckDB.Path != ""
		case "clickhouse":
			nestedConfigSet = len(output.ClickHouse.Addrs) > 0
		case "postgres":
			nestedConfigSet = output.Postgres.DSN != ""
		case "loki":
			nestedConfigSet = output.Loki.URL != ""
		case "otlp":
			nestedConfigSet = output.OTLP.Endpoint != ""
		case "s3":
			nestedConfigSet = output.S3.Bucket != ""
		case "gcs":
			nestedConfigSet = output.GCS.Bucket != ""
		default:
			t.Errorf("YAML example %d uses unsupported fanout type %q", i+1, output.Type)
			continue
		}
		if !nestedConfigSet {
			t.Errorf("YAML example %d does not populate the nested %q config", i+1, output.Type)
		}
	}
}
