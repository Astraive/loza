package commands

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/config"
)

var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var validEventID = regexp.MustCompile(`^[a-zA-Z0-9_:-]+$`)

// eventStringField returns the string value of key in a decoded event, or
// fallback when the key is absent or holds JSON null. Formatting a missing key
// directly would render the literal "<nil>" in streamed output. Present
// non-string values keep their previous formatting so no information is lost.
func eventStringField(ev map[string]any, key, fallback string) string {
	v, ok := ev[key]
	if !ok || v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// isValidIdentifier checks if a string is a safe SQL identifier (letters, digits, underscores; must start with letter or underscore).
func isValidIdentifier(s string) bool {
	return validIdentifier.MatchString(s)
}

// isValidEventID checks if a string is a safe event ID (letters, digits, underscores, hyphens, colons).
func isValidEventID(s string) bool {
	return validEventID.MatchString(s)
}

// validateReconstructionMode checks that a cortex reconstruction mode is one of
// the documented values. An empty mode is allowed so the server default is used.
func validateReconstructionMode(mode string) error {
	switch mode {
	case "", "fast", "deep":
		return nil
	default:
		return fmt.Errorf("invalid --mode %q: must be fast or deep", mode)
	}
}

func runCollectorConfigCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected collector config subcommand")
	}
	cmdArgs := append([]string{"config"}, args...)
	return client.RunCollectorCommand(ctx, cfg.CollectorRepoPath, cmdArgs)
}

func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeFileBytes(path string, data []byte) error {
	// Export output may contain sensitive data; keep it owner-only.
	return os.WriteFile(path, data, 0o600)
}
