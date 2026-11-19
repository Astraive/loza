package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/schema"
)

func SchemaCommand(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected subcommand: validate, fetch, list, diff, or publish")
	}

	switch args[0] {
	case "validate":
		fs := flag.NewFlagSet("schema validate", flag.ContinueOnError)
		strict := fs.Bool("strict", false, "enable strict validation (default: loose)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		remaining := fs.Args()
		if len(remaining) == 0 {
			if *strict {
				return fmt.Errorf("--strict requires a schema file argument: strict validation applies to event files, not spec-asset checks")
			}
			if err := schema.ValidateSpecAssets(cfg.SpecRepoPath); err != nil {
				return err
			}
			fmt.Println("Spec assets validated successfully")
			return nil
		}
		path := remaining[0]
		if err := schema.ValidateEventFileStrict(path, *strict); err != nil {
			return err
		}
		fmt.Println("Event validated successfully")
		return nil
	case "fetch":
		return schema.EnsureSpecAssets(cfg.SpecRepoPath)
	case "list":
		body, err := client.FetchSchema(cfg.CollectorURL)
		if err != nil {
			return err
		}
		fmt.Println(string(body))
		return nil
	case "diff":
		return runSchemaMutation(context.Background(), cfg, client.DiffSchema, args[1:])
	case "publish":
		return runSchemaMutation(context.Background(), cfg, client.PublishSchema, args[1:])
	case "blueprint":
		return runBlueprintCommand(context.Background(), cfg, args[1:])
	default:
		return fmt.Errorf("unknown schema subcommand: %s (expected: validate, fetch, list, diff, publish, blueprint)", args[0])
	}
}

func runBlueprintCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected blueprint subcommand: add, list")
	}

	switch args[0] {
	case "add":
		return runBlueprintAdd(ctx, cfg, args[1:])
	case "list":
		return runBlueprintList(ctx, cfg)
	default:
		return fmt.Errorf("unknown blueprint subcommand: %s (expected: add, list)", args[0])
	}
}

func runBlueprintAdd(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("blueprint add", flag.ContinueOnError)
	name := fs.String("name", "", "blueprint name")
	columns := fs.String("columns", "", "columns as name:type:path (comma-separated)")
	file := fs.String("file", "", "JSON file containing blueprint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		body, err := client.PublishBlueprint(ctx, cfg.CollectorURL, data)
		if err != nil {
			return err
		}
		fmt.Println(string(body))
		return nil
	}

	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	if *columns == "" {
		return fmt.Errorf("--columns is required (format: name:type:path,name:type:path)")
	}

	cols, err := parseBlueprintColumns(*columns)
	if err != nil {
		return err
	}
	blueprint := map[string]any{
		"name":    *name,
		"columns": cols,
	}

	data, err := json.Marshal(blueprint)
	if err != nil {
		return err
	}
	body, err := client.PublishBlueprint(ctx, cfg.CollectorURL, data)
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

// parseBlueprintColumns parses a comma-separated list of name:type:path column
// definitions into the columns map of a blueprint payload. Each component is
// trimmed and must be non-empty; the name must also be a valid identifier so
// the collector can apply it as a SQL column.
func parseBlueprintColumns(columnsCSV string) (map[string]any, error) {
	cols := make(map[string]any)
	for _, col := range strings.Split(columnsCSV, ",") {
		parts := strings.SplitN(col, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid column format: %s (expected name:type:path)", col)
		}
		name := strings.TrimSpace(parts[0])
		duckdbType := strings.TrimSpace(parts[1])
		jsonPath := strings.TrimSpace(parts[2])
		if name == "" || duckdbType == "" || jsonPath == "" {
			return nil, fmt.Errorf("invalid column format: %s (name, type, and path must not be empty)", col)
		}
		if !isValidIdentifier(name) {
			return nil, fmt.Errorf("invalid column name %q in %s (must be a valid identifier)", name, col)
		}
		cols[name] = map[string]string{
			"duckdb_type": duckdbType,
			"json_path":   jsonPath,
		}
	}
	return cols, nil
}

func runBlueprintList(ctx context.Context, cfg config.Config) error {
	body, err := client.ListBlueprints(ctx, cfg.CollectorURL)
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

// schemaMutationOp performs a schema mutation request against the collector.
// Both client.DiffSchema and client.PublishSchema satisfy this signature.
type schemaMutationOp func(ctx context.Context, baseURL string, payload []byte) ([]byte, error)

func runSchemaMutation(ctx context.Context, cfg config.Config, op schemaMutationOp, args []string) error {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	schemaVersion := fs.String("schema-version", "", "schema version")
	eventVersion := fs.String("event-version", "", "event version")
	required := fs.String("required", "", "comma-separated required fields")
	file := fs.String("file", "", "JSON file containing schema payload")
	if err := fs.Parse(args); err != nil {
		return err
	}

	payload, err := schemaMutationPayload(*schemaVersion, *eventVersion, *required, *file)
	if err != nil {
		return err
	}

	body, err := op(ctx, cfg.CollectorURL, payload)
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

func schemaMutationPayload(schemaVersion, eventVersion, requiredCSV, file string) ([]byte, error) {
	if strings.TrimSpace(file) != "" {
		return os.ReadFile(file)
	}
	fields := make([]string, 0)
	for _, part := range strings.Split(requiredCSV, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version":  strings.TrimSpace(schemaVersion),
		"event_version":   strings.TrimSpace(eventVersion),
		"required_fields": fields,
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}
