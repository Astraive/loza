package commands

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

func TailCommand(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	kindFilter := fs.String("kind", "", "filter by event kind")
	serviceFilter := fs.String("service", "", "filter by service name")
	levelFilter := fs.String("level", "", "filter by level")
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
		return fmt.Errorf("tail stream failed: %w", err)
	}
	defer conn.Close()

	output.PrintSection("Tailing events... (Ctrl+C to stop)")
	return streamLines(ctx, conn)
}

// streamLines is the tail-specific adapter around the shared stream parser.
// Keeping this signature preserves the internal API exercised by tail tests.
func streamLines(ctx context.Context, body io.Reader) error {
	return streamEvents(body, streamOptions{ctx: ctx, extraFields: tailExtraFields})
}
