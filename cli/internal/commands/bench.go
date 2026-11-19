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

	// Reject rate/duration combinations whose event count overflows the
	// platform's int range instead of silently using a wrapped-around count.
	if _, err := computeTotalEvents(*rate, *duration); err != nil {
		return err
	}
	// Compute the count with rounding so fractional and sub-second durations
	// yield the accurate total (e.g. 100 events/sec over 500ms is 50, not 0).
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

// computeTotalEvents returns the number of events a benchmark run should send.
// The multiplication is carried out in int64 with an explicit check against
// the platform's int limit so that large rate/duration combinations are
// rejected instead of wrapping around to a wrong (possibly small positive)
// event count.
func computeTotalEvents(rate int, duration time.Duration) (int, error) {
	if rate <= 0 {
		return 0, fmt.Errorf("rate must be greater than zero")
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be greater than zero")
	}

	seconds := int64(duration / time.Second)
	if seconds == 0 {
		// Sub-second durations produce no whole-seconds worth of events;
		// keep the historical behavior of sending a single second of events.
		return rate, nil
	}

	maxInt := int64(math.MaxInt)
	if int64(rate) > maxInt/seconds {
		return 0, fmt.Errorf("rate %d over duration %s produces too many events (exceeds int range)", rate, duration)
	}

	return int(int64(rate) * seconds), nil
}
