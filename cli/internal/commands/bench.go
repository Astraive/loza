package commands

import (
	"context"
	"flag"
	"fmt"
	"math"
	"time"

	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/loadgen"
	"github.com/astraive/loza/cli/internal/output"
)

func BenchCommand(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	url := fs.String("url", cfg.CollectorURL, "collector URL")
	rate := fs.Int("rate", 100, "events per second")
	duration := fs.Duration("duration", 10*time.Second, "benchmark duration")
	size := fs.Int("size", 1024, "payload size in bytes")
	batchSize := fs.Int("batch-size", 10, "events per batch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *rate <= 0 {
		return fmt.Errorf("--rate must be positive, got %d", *rate)
	}
	if *duration <= 0 {
		return fmt.Errorf("--duration must be positive, got %v", *duration)
	}
	if *size <= 0 {
		return fmt.Errorf("--size must be positive, got %d", *size)
	}
	if *batchSize <= 0 {
		return fmt.Errorf("--batch-size must be positive, got %d", *batchSize)
	}

	totalEvents := benchTotalEvents(*rate, *duration)
	workers := 4
	if *rate < workers {
		workers = max(*rate, 1)
	}

	output.PrintSection("Load Benchmark")
	fmt.Printf("  Target:      %s\n", *url)
	fmt.Printf("  Rate:        %d events/sec\n", *rate)
	fmt.Printf("  Duration:    %v\n", *duration)
	fmt.Printf("  Total:       %d events\n", totalEvents)
	fmt.Printf("  Workers:     %d\n", workers)
	fmt.Printf("  Batch size:  %d\n", *batchSize)
	fmt.Printf("  Payload:     %d bytes\n", *size)
	fmt.Println()

	runnerCfg := loadgen.RunnerConfig{
		URL:       *url,
		Events:    totalEvents,
		Workers:   workers,
		BatchSize: *batchSize,
		BodySize:  *size,
	}

	report, err := loadgen.Run(ctx, runnerCfg)
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}
	report.Print()
	return nil
}

// benchTotalEvents returns the number of events to send for a benchmark at the
// given rate and duration. It computes with floating-point math and rounding so
// fractional and sub-second durations yield the correct count (for example,
// 100 events/sec for 500ms is 50 events, not 0). A non-positive result falls
// back to the rate, preserving the previous behavior for a zero duration.
func benchTotalEvents(rate int, duration time.Duration) int {
	total := int(math.Round(float64(rate) * duration.Seconds()))
	if total <= 0 {
		return rate
	}
	return total
}
