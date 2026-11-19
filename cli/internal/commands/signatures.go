package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

func SignaturesCommand(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("signatures", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "max signatures to list")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cortexURL := getCortexURL(cfg)
	data, err := client.FetchSignatures(ctx, cortexURL, *limit)
	if err != nil {
		return fmt.Errorf("fetch signatures: %w", err)
	}

	if output.ShouldOutputJSON(ctx) {
		fmt.Println(string(data))
		return nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		fmt.Println(string(data))
		return nil
	}

	dataObj, ok := result["data"].(map[string]any)
	if !ok {
		fmt.Println(string(data))
		return nil
	}

	sigs, ok := dataObj["signatures"].([]any)
	if !ok {
	sigs, err := parseSignaturesResponse(data)
	if err != nil {
		fmt.Println(string(data))
		return err
	}

	rows := [][]string{}
	for _, s := range sigs {
		if m, ok := s.(map[string]any); ok {
			rows = append(rows, []string{
				fmt.Sprintf("%v", m["id"]),
				fmt.Sprintf("%v", m["pattern"]),
				fmt.Sprintf("%v", m["count"]),
				fmt.Sprintf("%v", m["lastSeen"]),
			})
		}
	}
	output.PrintSection("Incident Signatures")
	output.PrintTable([]string{"ID", "Pattern", "Count", "Last Seen"}, rows)
	return nil
}

// parseSignaturesResponse extracts the signatures array from a signatures
// API response, returning a descriptive error instead of panicking when the
// response is malformed or has an unexpected shape.
func parseSignaturesResponse(data []byte) ([]any, error) {
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode signatures response: %w", err)
	}
	payload, ok := result["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("signatures response: missing or invalid 'data' object")
	}
	sigs, ok := payload["signatures"].([]any)
	if !ok {
		return nil, fmt.Errorf("signatures response: missing or invalid 'signatures' array")
	}
	return sigs, nil
}
