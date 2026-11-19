package commands

import (
	"context"
	"flag"
	"fmt"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

func WatchCommand(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	kindFilter := fs.String("kind", "", "filter by event kind")
	serviceFilter := fs.String("service", "", "filter by service name")
	levelFilter := fs.String("level", "", "filter by level (debug, info, warn, error)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	filters := map[string]string{}
	if *kindFilter != "" {
		filters["kind"] = *kindFilter
	}
	if *serviceFilter != "" {
		filters["service"] = *serviceFilter
	}
	if *levelFilter != "" {
		filters["level"] = *levelFilter
	}

	conn, err := client.WatchStream(ctx, cfg.CollectorURL, filters)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer conn.Close()

	fmt.Println(output.Bold("Watching events... (Ctrl+C to stop)"))
	return streamEvents(conn, streamOptions{})
}
