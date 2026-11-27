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

	// The runner count and its overflow check must use the same rounded,
	// duration-precise calculation.
	totalEvents, err := computeTotalEvents(*rate, *duration)
	if err != nil {
		return err
	}

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

// computeTotalEvents returns the rounded number of events a benchmark run
// should send. It keeps duration precision while checking the result against
// the platform's int range.
func computeTotalEvents(rate int, duration time.Duration) (int, error) {
	if rate <= 0 {
		return 0, fmt.Errorf("rate must be greater than zero")
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be greater than zero")
	}

	const nanosPerSecond = int64(time.Second)
	rate64 := int64(rate)
	seconds := int64(duration / time.Second)
	nanos := int64(duration % time.Second)
	maxInt := int64(math.MaxInt)
	if seconds > 0 && rate64 > maxInt/seconds {
		return 0, fmt.Errorf("rate %d over duration %s produces too many events (exceeds int range)", rate, duration)
	}
	total := rate64 * seconds

	// Divide rate*nanos without overflowing int64: the low product is below
	// 1e18 because both factors are less than 1e9.
	remainderRate := rate64 % nanosPerSecond
	fraction := (rate64/nanosPerSecond)*nanos + (remainderRate*nanos)/nanosPerSecond
	if total > maxInt-fraction {
		return 0, fmt.Errorf("rate %d over duration %s produces too many events (exceeds int range)", rate, duration)
	}
	total += fraction
	if (remainderRate*nanos)%nanosPerSecond >= nanosPerSecond/2 {
		if total == maxInt {
			return 0, fmt.Errorf("rate %d over duration %s produces too many events (exceeds int range)", rate, duration)
		}
		total++
	}

	// Preserve the previous benchmark behavior for positive durations whose
	// rounded event count is zero.
	if total <= 0 {
		return rate, nil
	}
	return int(total), nil
}
