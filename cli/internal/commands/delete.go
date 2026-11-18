package commands

import (
	"context"
	"flag"
	"fmt"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
)

func DeleteCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected delete target: tenant, user, or event")
	}

	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	reason := fs.String("reason", "", "optional audit reason for the deletion")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("expected identifier for delete target %q", args[0])
	}

	identifier := rest[0]
	// The identifier is concatenated into a request URL path by the client,
	// so reject anything outside the safe allowlist before using it. Without
	// this check, characters such as ?, #, / or .. could alter the path/query
	// and cause the CLI argument to target a different endpoint.
	if !isValidEventID(identifier) {
		return fmt.Errorf("invalid identifier for delete target %q: must match [a-zA-Z0-9_:-]+", args[0])
	}

	var (
		body []byte
		err  error
	)
	switch args[0] {
	case "tenant":
		body, err = client.DeleteEventsByTenant(ctx, cfg.CollectorURL, identifier, *reason)
	case "user":
		body, err = client.DeleteEventsByUser(ctx, cfg.CollectorURL, identifier, *reason)
	case "event":
		body, err = client.DeleteEventByID(ctx, cfg.CollectorURL, identifier, *reason)
	default:
		return fmt.Errorf("unknown delete target: %s", args[0])
	}
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}
