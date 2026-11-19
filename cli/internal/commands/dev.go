package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/astraive/loza/cli/internal/config"
)

func DevCommand(ctx context.Context, cfg config.Config, _ []string) error {
	collectorBin := filepath.Join(cfg.CollectorRepoPath, "cmd", "loza-collector", "main.go")
	if _, err := os.Stat(collectorBin); err != nil {
		return fmt.Errorf("collector not found at %s", cfg.CollectorRepoPath)
	}

	collectorCmd := exec.CommandContext(ctx, "go", "run", collectorBin, "-c", configPath(cfg, "loza.local.yaml"))
	collectorCmd.Stdout = os.Stdout
	collectorCmd.Stderr = os.Stderr
	collectorCmd.Dir = cfg.CollectorRepoPath

	if strings.TrimSpace(cfg.CortexRepoPath) == "" {
		fmt.Println("Starting collector...")
		return collectorCmd.Run()
	}

	cortexMain := filepath.Join(cfg.CortexRepoPath, "cmd", "server", "main.go")
	if _, err := os.Stat(cortexMain); err != nil {
		fmt.Println("Starting collector...")
		return collectorCmd.Run()
	}

	fmt.Println("Starting collector...")
	if err := collectorCmd.Start(); err != nil {
		return fmt.Errorf("start collector: %w", err)
	}

	// Wait for collector to be ready before starting cortex
	collectorURL := collectorHealthURL(cfg)
	// Wait for collector to be ready before starting cortex.
	// Bound every probe with both a client timeout and the caller's context.
	collectorURL := "http://localhost:9308/health"
	probeClient := &http.Client{Timeout: 2 * time.Second}
	fmt.Println("Waiting for collector to be ready...")
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, collectorURL, nil)
		if reqErr == nil {
			resp, err := probeClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
		}
		if i == 29 {
			stopCollector(collectorCmd)
			return fmt.Errorf("collector did not become ready within 30s")
		}
	}

	fmt.Println("Starting cortex...")
	cortexCmd := exec.CommandContext(ctx, "go", "run", cortexMain, "--config", filepath.Join(cfg.CortexRepoPath, "configs", "loza-cortex.defaults.yaml"))
	cortexCmd.Stdout = os.Stdout
	cortexCmd.Stderr = os.Stderr
	cortexCmd.Dir = cfg.CortexRepoPath
	return superviseDev(collectorCmd, cortexCmd)
}

// stopCollector terminates and reaps an already-started collector exactly
// once. Cmd.Wait reaps the child, so Process.Wait must not be used as well.
func stopCollector(collectorCmd *exec.Cmd) {
	if collectorCmd.Process != nil {
		_ = collectorCmd.Process.Kill()
	}
	_ = collectorCmd.Wait()
}

// superviseDev runs cortex to completion and then tears down the collector.
// The collector is a long-running server that never exits on its own, so it
// must be killed before waiting; otherwise DevCommand blocks forever in Wait
// and the cleanup is unreachable. The deferred stop also covers the case
// where cortex fails to start or returns an error.
func superviseDev(collectorCmd, cortexCmd *exec.Cmd) error {
	defer stopCollector(collectorCmd)
	if err := cortexCmd.Run(); err != nil {
		return fmt.Errorf("run cortex: %w", err)
	}
	return nil
}

// collectorHealthURL derives the readiness probe URL from the configured
// collector endpoint. The collector can be launched with a non-default listen
// address/port (via its config file), so probing a hardcoded localhost:9308
// would never succeed. Fall back to the default port only when no URL is set.
func collectorHealthURL(cfg config.Config) string {
	base := strings.TrimSpace(cfg.CollectorURL)
	if base == "" {
		base = "http://localhost:9308"
	}
	return strings.TrimRight(base, "/") + "/health"
}

func configPath(cfg config.Config, name string) string {
	return filepath.Join(cfg.CollectorRepoPath, "configs", name)
}
