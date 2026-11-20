package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	lozacollector "github.com/astraive/loza/collector"
)

var version = lozacollector.Version

type Config struct {
	URL       string
	Events    int
	Workers   int
	BatchSize int
	APIKey    string
	BodySize  int
}

type Stats struct {
	accepted int64
	invalid  int64
	rejected int64
	errors   int64
	duration time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "loza-loadgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg := Config{
		URL:       "http://localhost:9308/ingest",
		Events:    10000,
		Workers:   4,
		BatchSize: 100,
		APIKey:    "",
		BodySize:  512,
	}

	fs := flag.NewFlagSet("loza-loadgen", flag.ContinueOnError)
	url := fs.String("url", cfg.URL, "collector URL")
	events := fs.Int("events", cfg.Events, "total events to send")
	workers := fs.Int("workers", cfg.Workers, "concurrent workers")
	batchSize := fs.Int("batch", cfg.BatchSize, "batch size per request")
	apiKey := fs.String("api-key", cfg.APIKey, "API key for auth")
	bodySize := fs.Int("body-size", cfg.BodySize, "average body size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg.URL = *url
	cfg.Events = *events
	cfg.Workers = *workers
	cfg.BatchSize = *batchSize
	cfg.APIKey = *apiKey
	cfg.BodySize = *bodySize

	if err := validateConfig(cfg); err != nil {
		return err
	}

	fmt.Printf("LOZA Load Generator v%s\n", version)
	fmt.Printf("Target: %s\n", cfg.URL)
	fmt.Printf("Events: %d, Workers: %d, Batch: %d\n", cfg.Events, cfg.Workers, cfg.BatchSize)
	fmt.Println()

	return runLoadTest(cfg)
}

// validateConfig rejects flag combinations that would otherwise cause the
// load generator to panic (e.g. dividing the event count by zero workers).
func validateConfig(cfg Config) error {
	if cfg.Workers <= 0 {
		return fmt.Errorf("workers must be greater than 0, got %d", cfg.Workers)
	}
	if cfg.Events < 0 {
		return fmt.Errorf("events must be non-negative, got %d", cfg.Events)
	}
	return nil
}

func runLoadTest(cfg Config) error {
	client := &http.Client{Timeout: 30 * time.Second}
	stats := &Stats{}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	start := time.Now()

	var wg sync.WaitGroup

	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, client, cfg, workerID, workerEventCount(cfg.Events, cfg.Workers, workerID), stats)
		}(w)
	}

	// Wait for completion or cancellation
	wg.Wait()

	stats.duration = time.Since(start)

	// Print results
	fmt.Println()
	fmt.Println("=== Results ===")
	fmt.Printf("Duration:    %v\n", stats.duration)
	fmt.Printf("Accepted:    %d\n", stats.accepted)
	fmt.Printf("Invalid:     %d\n", stats.invalid)
	fmt.Printf("Rejected:    %d\n", stats.rejected)
	fmt.Printf("Errors:      %d\n", stats.errors)
	fmt.Printf("Throughput:  %s events/sec\n", formatThroughput(stats.accepted, stats.duration))

	return nil
}

// workerEventCount returns the number of events assigned to the worker with the
// given index. Events are split evenly across workers, and any remainder from
// events/workers is handed out one apiece to the first workers, so the counts
// across all workers always sum to exactly events. This also covers the case
// where events < workers (some workers receive zero events).
func workerEventCount(events, workers, workerID int) int {
	if workers <= 0 {
		return 0
	}
	count := events / workers
	if workerID < events%workers {
		count++
	}
	return count
}

// formatThroughput renders the events/sec rate for the report. A non-positive
// duration (for example a degenerate run started with -events 0) would
// otherwise make the division yield NaN or +Inf, so it is reported as "n/a".
func formatThroughput(accepted int64, d time.Duration) string {
	if secs := d.Seconds(); secs > 0 {
		return fmt.Sprintf("%.2f", float64(accepted)/secs)
	}
	return "n/a"
}

func runWorker(ctx context.Context, client *http.Client, cfg Config, workerID, count int, stats *Stats) {
	rng := rand.New(rand.NewSource(int64(workerID)))
	batch := make([][]byte, 0, cfg.BatchSize)

	for i := 0; i < count && ctx.Err() == nil; i++ {
		event := generateEvent(rng, cfg.BodySize)
		batch = append(batch, event)

		if len(batch) >= cfg.BatchSize || i == count-1 {
			sendBatch(ctx, client, cfg, batch, stats)
			batch = batch[:0]
		}
	}
}

func generateEvent(rng *rand.Rand, bodySize int) []byte {
	event := map[string]any{
		"event_id":    fmt.Sprintf("evt-%d-%d", rng.Int63(), time.Now().UnixNano()),
		"event":       "loadtest.event",
		"service":     "loadtest",
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"method":      "GET",
		"path":        fmt.Sprintf("/api/test/%d", rng.Intn(1000)),
		"status_code": 200,
		"duration_ms": rng.Intn(500),
		"user_id":     fmt.Sprintf("user-%d", rng.Intn(10000)),
		"payload":     randomString(rng, bodySize),
	}
	data, _ := json.Marshal(event)
	return data
}

func randomString(rng *rand.Rand, n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

func sendBatch(ctx context.Context, client *http.Client, cfg Config, batch [][]byte, stats *Stats) {
	if len(batch) == 0 {
		return
	}

	body, _ := json.Marshal(map[string]any{"events": batch})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		atomic.AddInt64(&stats.errors, int64(len(batch)))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		atomic.AddInt64(&stats.errors, int64(len(batch)))
		return
	}
	defer resp.Body.Close()

	var result struct {
		Accepted int `json:"accepted"`
		Invalid  int `json:"invalid"`
		Rejected int `json:"rejected"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	atomic.AddInt64(&stats.accepted, int64(result.Accepted))
	atomic.AddInt64(&stats.invalid, int64(result.Invalid))
	atomic.AddInt64(&stats.rejected, int64(result.Rejected))
}
