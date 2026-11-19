package commands

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/astraive/loza/cli/internal/config"
)

func InitCommand(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	valMode := fs.String("validation-mode", "", "validation mode: off, warn, enforce, quarantine")
	force := fs.Bool("force", false, "overwrite the target file if it already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *valMode != "" {
		cfg.ValidationMode = strings.ToLower(strings.TrimSpace(*valMode))
	}
	target := ".loza-cli.yaml"
	if fs.NArg() > 0 && fs.Arg(0) != "" {
		target = fs.Arg(0)
	}
	if err := config.ValidateValidationMode(cfg.ValidationMode); err != nil {
		return err
	}
	cleanTarget := filepath.Clean(target)
	if _, err := os.Stat(target); err == nil {
		if !*force {
			return fmt.Errorf("target %s already exists; use --force to overwrite", cleanTarget)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check target %s: %w", cleanTarget, err)
	}
	if err := config.Generate(target, cfg); err != nil {
		return err
	}
	fmt.Printf("initialized %s\n", cleanTarget)
	return nil
}
