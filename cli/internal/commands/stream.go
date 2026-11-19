package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/astraive/loza/cli/internal/output"
)

const (
	streamInitialBuffer = 64 * 1024
	streamMaxTokenSize  = 1024 * 1024
)

// streamExtraField describes an optional event field appended after the
// standard timestamp/level/service/event columns by streamEvents.
type streamExtraField struct {
	key    string
	asJSON bool // marshal the value as JSON (used for nested values such as "errors")
}

// streamOptions parameterizes the shared stream parser/formatter. A non-nil
// ctx makes the loop stop promptly on cancellation; extraFields are appended
// to each formatted event line.
type streamOptions struct {
	ctx         context.Context
	extraFields []streamExtraField
}

// tailExtraFields are the additional columns the tail command renders.
var tailExtraFields = []streamExtraField{
	{key: "release"},
	{key: "trace_flags"},
	{key: "errors", asJSON: true},
}

// streamEvents is the single newline-delimited event parser/formatter shared
// by the watch and tail commands. It colourizes JSON events by level, echoes
// non-JSON lines verbatim, and returns scanner.Err() so both callers surface
// read failures consistently.
func streamEvents(body io.Reader, opts streamOptions) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, streamInitialBuffer), streamMaxTokenSize)
	for scanner.Scan() {
		if opts.ctx != nil {
			select {
			case <-opts.ctx.Done():
				return nil
			default:
			}
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			fmt.Println(line)
			continue
		}
		printStreamEvent(ev, opts.extraFields)
	}
	return scanner.Err()
}

// printStreamEvent writes one decoded event using the shared level colouring
// and any configured extra fields.
func printStreamEvent(ev map[string]any, extras []streamExtraField) {
	// Use eventStringField so missing/null columns render as sensible
	// placeholders ("unknown" for the level) instead of the literal "<nil>".
	level := eventStringField(ev, "level", "unknown")
	ts := eventStringField(ev, "timestamp", "")
	svc := eventStringField(ev, "service", "")
	evt := eventStringField(ev, "event", "")

	switch level {
	case "error":
		fmt.Printf("%s %s %s %s", output.Dim(ts), output.Error(level), output.Bold(svc), evt)
	case "warn":
		fmt.Printf("%s %s %s %s", output.Dim(ts), output.Warning(level), output.Bold(svc), evt)
	default:
		fmt.Printf("%s %s %s %s", output.Dim(ts), output.Info(level), output.Bold(svc), evt)
	}

	for _, extra := range extras {
		v, ok := ev[extra.key]
		if !ok {
			continue
		}
		if extra.asJSON {
			if b, err := json.Marshal(v); err == nil {
				fmt.Printf(" %s=%s", extra.key, b)
				continue
			}
		}
		fmt.Printf(" %s=%v", extra.key, v)
	}
	fmt.Println()
}
