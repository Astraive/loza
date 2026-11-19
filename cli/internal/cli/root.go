package cli

import (
	"context"
	"fmt"
	"strings"

	lozacli "github.com/astraive/loza/cli"
	"github.com/astraive/loza/cli/internal/client"
	"github.com/astraive/loza/cli/internal/commands"
	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

var version = lozacli.Version

var CommandMaturity = map[string]string{
	"init":       "stable",
	"cortex":     "beta",
	"dev":        "stable",
	"config":     "stable",
	"schema":     "stable",
	"collector":  "stable",
	"worker":     "beta",
	"emit":       "stable",
	"query":      "stable",
	"tail":       "stable",
	"watch":      "experimental",
	"status":     "stable",
	"sinks":      "beta",
	"dlq":        "beta",
	"quarantine": "beta",
	"keys":       "beta",
	"export":     "experimental",
	"replay":     "beta",
	"delete":     "beta",
	"audit":      "beta",
	"doctor":     "experimental",
	"bench":      "stable",
	"deploy":     "beta",
	"dashboard":  "experimental",
	"incident":   "beta",
	"graph":      "experimental",
	"signatures": "experimental",
	"debug":      "experimental",
}

// isGlobalOutputFormat reports whether value is one of the formats accepted by
// the global --output flag. Subcommands reuse --output for file paths (for
// example `emit sample --output envelope.json`); a value that is not a known
// format must therefore be left in place for the subcommand's own flag set.
func isGlobalOutputFormat(value string) bool {
	switch value {
	case "json", "table", "text", "yaml":
		return true
	}
	return false
}

// extractGlobalFlags removes the global flags from args and returns the
// remaining positional arguments. The space form (--output FORMAT) and the
// equals form (--output=FORMAT) are parsed in this one shared place so that
// both accept any format value and behave identically.
//
// This combines two behaviors: the global --output flag is recognized
// generically (any value is accepted for either form), while a subcommand's
// own --output <path> flag is still preserved once a command has been seen and
// the value is not a known global format (e.g. `emit sample --output x.json`).
func extractGlobalFlags(args []string) (verbose bool, outputFmt string, filtered []string) {
	outputFmt = "text"
	commandSeen := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--verbose":
			verbose = true
		case args[i] == "--output":
			if i+1 < len(args) {
				value := args[i+1]
				if isGlobalOutputFormat(value) || !commandSeen {
					// Known global format, or still before the command: consume
					// the value as the global output format (any value is valid).
					i++
					outputFmt = value
				} else {
					// Subcommand output path: leave the flag for the subcommand.
					filtered = append(filtered, args[i])
				}
			} else {
				// Dangling --output flag: leave it for the subcommand.
				filtered = append(filtered, args[i])
			}
		case strings.HasPrefix(args[i], "--output="):
			_, outputFmt, _ = strings.Cut(args[i], "--output=")
		default:
			filtered = append(filtered, args[i])
			commandSeen = true
		}
	}
	return verbose, outputFmt, filtered
}

// splitGlobalFlags is kept for callers that expect main's
// (args, verbose, format) ordering. It delegates to the shared parser so both
// entry points apply identical semantics.
func splitGlobalFlags(args []string) ([]string, bool, string) {
	verbose, outputFmt, filtered := extractGlobalFlags(args)
	return filtered, verbose, outputFmt
}

func Run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	verbose, outputFmt, filtered := extractGlobalFlags(args)
	args = filtered

	if len(args) == 0 {
		printHelp()
		return nil
	}

	ctx := context.Background()
	if verbose {
		ctx = output.WithVerbose(ctx)
	}
	ctx = output.WithFormat(ctx, outputFmt)

	switch args[0] {
	case "version", "-v", "--version":
		fmt.Println("loza version", version)
		return nil
	case "help", "--help", "-h":
		printHelp()
		return nil
	case "maturity":
		printMaturity()
		return nil
	case "init":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load cli config: %w", err)
		}
		return commands.InitCommand(cfg, args[1:])
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load cli config: %w", err)
	}

	// Seed the client package with the config-file API key so all HTTP
	// calls can fall back to it when no env-var key is set.
	if cfg.Cortex != nil && cfg.Cortex.APIKey != "" {
		client.SetConfigAPIKey(cfg.Cortex.APIKey)
	}

	switch args[0] {
	case "cortex":
		return commands.CortexCommand(ctx, cfg, args[1:])
	case "dev":
		return commands.DevCommand(ctx, cfg, args[1:])
	case "config":
		return commands.ConfigCommand(ctx, cfg, args[1:])
	case "schema":
		return commands.SchemaCommand(cfg, args[1:])
	case "collector":
		return commands.CollectorCommand(ctx, cfg, args[1:])
	case "worker":
		return commands.WorkerCommand(ctx, cfg, args[1:])
	case "emit":
		return commands.EmitCommand(cfg, args[1:])
	case "query":
		return commands.QueryCommand(ctx, cfg, args[1:])
	case "tail":
		return commands.TailCommand(ctx, cfg, args[1:])
	case "watch":
		return commands.WatchCommand(ctx, cfg, args[1:])
	case "status":
		return commands.StatusCommand(ctx, cfg, args[1:])
	case "sinks":
		return commands.SinksCommand(ctx, cfg, args[1:])
	case "dlq":
		return commands.DLQCommand(ctx, cfg, args[1:])
	case "export":
		return commands.ExportCommand(cfg, args[1:])
	case "replay":
		return commands.ReplayCommand(cfg, args[1:])
	case "delete":
		return commands.DeleteCommand(ctx, cfg, args[1:])
	case "audit":
		return commands.AuditCommand(ctx, cfg, args[1:])
	case "doctor":
		return commands.DoctorCommand(ctx, cfg, args[1:])
	case "bench":
		return commands.BenchCommand(ctx, cfg, args[1:])
	case "deploy":
		return commands.DeployCommand(cfg, args[1:])
	case "dashboard":
		return commands.DashboardCommand(cfg, args[1:])
	case "incident":
		return commands.IncidentCommand(ctx, cfg, args[1:])
	case "graph":
		return commands.GraphCommand(ctx, cfg, args[1:])
	case "signatures":
		return commands.SignaturesCommand(ctx, cfg, args[1:])
	case "quarantine":
		return commands.QuarantineCommand(ctx, cfg, args[1:])
	case "keys":
		return commands.KeysCommand(ctx, cfg, args[1:])
	case "debug":
		return commands.DebugCommand(ctx, cfg, args[1:])
	default:
		return fmt.Errorf("unknown command: %s\nRun 'loza help' for available commands", args[0])
	}
}

func printHelp() {
	output.PrintSection("LOZA CLI v" + version)
	fmt.Println("\nData Plane (collector):")
	fmt.Println("  emit         Emit events to collector")
	fmt.Println("  query        Query events via SQL")
	fmt.Println("  tail         Stream events from collector")
	fmt.Println("  watch        Live event viewer with filters")
	fmt.Println("  status       Show collector status")
	fmt.Println("  sinks        Inspect sink health")
	fmt.Println("  dlq          Dead letter queue management")
	fmt.Println("  export       Export events to file")
	fmt.Println("  replay       Replay events from file")
	fmt.Println("  delete       GDPR event deletion")
	fmt.Println("  audit        PII audit scanning")
	fmt.Println("  schema       Schema validation and governance")
	fmt.Println("\nControl Plane (cortex):")
	fmt.Println("  cortex       Cortex operational memory commands")
	fmt.Println("  incident     View incident details")
	fmt.Println("  graph        Service/incident dependency graphs")
	fmt.Println("  signatures   Incident signature catalog")
	fmt.Println("\nOperations:")
	fmt.Println("  dev          Start development server")
	fmt.Println("  config       Configuration management")
	fmt.Println("  collector    Manage collector binary")
	fmt.Println("  worker       Manage worker binary")
	fmt.Println("  quarantine   Manage quarantined events")
	fmt.Println("  keys         API key management")
	fmt.Println("  bench        Load generation benchmark")
	fmt.Println("  deploy       Deployment asset management")
	fmt.Println("  dashboard    Grafana dashboard management")
	fmt.Println("  doctor       Health checks on all components")
	fmt.Println("  debug        Debug and diagnostics")
	fmt.Println("\nGlobal Flags:")
	fmt.Println("  --verbose         Show detailed output")
	fmt.Println("  --output FORMAT   Output format: json, table, text")
	fmt.Println("\nUse 'loza <command> --help' for details")
}

func printMaturity() {
	output.PrintSection("LOZA CLI Command Maturity")
	for cmd, stability := range CommandMaturity {
		label := stability
		switch stability {
		case "stable":
			label = output.Success(stability)
		case "beta":
			label = output.Warning(stability)
		case "experimental":
			label = output.Error(stability)
		}
		fmt.Printf("  %-12s [%s]\n", cmd, label)
	}
	fmt.Println("\nMaturity Levels:")
	fmt.Printf("  %-12s %s\n", "stable", "Production-ready, covered by release tests")
	fmt.Printf("  %-12s %s\n", "beta", "Working, being refined, may have minor changes")
	fmt.Printf("  %-12s %s\n", "experimental", "Under active development, subject to change")
}
