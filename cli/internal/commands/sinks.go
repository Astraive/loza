package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

// validSinkName restricts sink names to a single safe URL path segment so a
// user-supplied name cannot introduce extra path segments ("../", "a/b"), a
// query ("?") or a fragment ("#") into the collector endpoint.
var validSinkName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateSinkName(name string) error {
	if !validSinkName.MatchString(name) {
		return fmt.Errorf("invalid sink name %q: must start with a letter or digit and contain only letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

func SinksCommand(ctx context.Context, cfg config.Config, args []string) error {
	sub := "list"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "list":
		return sinksList(ctx, cfg)
	case "show":
		if len(args) == 0 {
			return fmt.Errorf("usage: loza sinks show <name>")
		}
		return sinksShow(ctx, cfg, args[0])
	case "test":
		if len(args) == 0 {
			return fmt.Errorf("usage: loza sinks test <name>")
		}
		return sinksTest(ctx, cfg, args[0])
	default:
		return fmt.Errorf("unknown sinks subcommand: %s", sub)
	}
}

func sinksList(ctx context.Context, cfg config.Config) error {
	body, err := client.FetchSinks(cfg.CollectorURL)
	if err != nil {
		return fmt.Errorf("fetch sinks: %w", err)
	}

	if output.ShouldOutputJSON(ctx) {
		fmt.Println(string(body))
		return nil
	}

	var result struct {
		Sinks []map[string]any `json:"sinks"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println(string(body))
		return nil
	}

	rows := [][]string{}
	for _, s := range result.Sinks {
		name := fmt.Sprintf("%v", s["name"])
		status := fmt.Sprintf("%v", s["status"])
		circuit := fmt.Sprintf("%v", s["circuit_state"])
		rows = append(rows, []string{name, status, circuit})
	}
	output.PrintTable([]string{"Name", "Status", "Circuit"}, rows)
	return nil
}

func sinksShow(ctx context.Context, cfg config.Config, name string) error {
	if err := validateSinkName(name); err != nil {
		return err
	}
	body, err := client.FetchSinkHealth(cfg.CollectorURL, url.PathEscape(name))
	if err != nil {
		return fmt.Errorf("fetch sink %s: %w", name, err)
	}

	if output.ShouldOutputJSON(ctx) {
		fmt.Println(string(body))
		return nil
	}

	var sink map[string]any
	if err := json.Unmarshal(body, &sink); err != nil {
		fmt.Println(string(body))
		return nil
	}

	output.PrintSection("Sink: " + name)
	pairs := map[string]string{}
	for k, v := range sink {
		pairs[k] = fmt.Sprintf("%v", v)
	}
	output.PrintKeyValue(pairs)
	return nil
}

func sinksTest(ctx context.Context, cfg config.Config, name string) error {
	if err := validateSinkName(name); err != nil {
		return err
	}
	body, err := client.TestSink(ctx, cfg.CollectorURL, url.PathEscape(name))
	if err != nil {
		return fmt.Errorf("test sink %s: %w", name, err)
	}

	if output.ShouldOutputJSON(ctx) {
		fmt.Println(string(body))
		return nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println(string(body))
		return nil
	}

	output.PrintSection("Sink test: " + name)
	pairs := map[string]string{}
	for k, v := range result {
		pairs[k] = fmt.Sprintf("%v", v)
	}
	output.PrintKeyValue(pairs)
	return nil
}
