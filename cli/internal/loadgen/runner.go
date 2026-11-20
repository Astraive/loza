package loadgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RunnerConfig struct {
	URL       string
	Events    int
	Workers   int
	BatchSize int
	BodySize  int
}

// rejectionError reports a batch the server explicitly refused with a 4xx
// status code. It lets the runner tell a rejection apart from a transport or
// server-side failure so Report.Rejected is populated correctly.
type rejectionError struct {
	statusCode int
}

func (e *rejectionError) Error() string {
	return fmt.Sprintf("loadgen post rejected with %d", e.statusCode)
}

// Validate checks numeric inputs before a run is started. Running with these
// values would otherwise cause panics (for example dividing by zero workers)
// or nonsensical results.
func (cfg RunnerConfig) Validate() error {
	switch {
	case cfg.Workers < 1:
		return fmt.Errorf("workers must be at least 1")
	case cfg.Events < 1:
		return fmt.Errorf("events must be at least 1")
	case cfg.BatchSize < 1:
		return fmt.Errorf("batch size must be at least 1")
	case cfg.BodySize < 0:
		return fmt.Errorf("body size must not be negative")
	}
	return nil
}

func Run(ctx context.Context, cfg RunnerConfig) (Report, error) {
	if err := cfg.Validate(); err != nil {
		return Report{}, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var accepted atomic.Int64
	var rejected atomic.Int64
	var errorsCount atomic.Int64

	start := time.Now()
	var wg sync.WaitGroup
	eventsPerWorker := cfg.Events / cfg.Workers
	extra := cfg.Events % cfg.Workers
	if eventsPerWorker == 0 {
		eventsPerWorker = cfg.Events
		cfg.Workers = 1
		extra = 0
	}

	for workerID := 0; workerID < cfg.Workers; workerID++ {
		wg.Add(1)
		count := eventsPerWorker
		if workerID < extra {
			count++
		}
		go func(id int, count int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + time.Now().UnixNano()))
			batch := make([][]byte, 0, cfg.BatchSize)
			for i := 0; i < count && ctx.Err() == nil; i++ {
				event, err := GenerateEvent(rng, cfg.BodySize)
				if err != nil {
					errorsCount.Add(1)
					continue
				}
				batch = append(batch, event)
				if len(batch) >= cfg.BatchSize || i == count-1 {
					flushBatch(ctx, client, cfg, batch, &accepted, &rejected, &errorsCount)
					batch = batch[:0]
				}
			}
			// The loop may exit with events still buffered (for example when
			// the context is cancelled mid-batch, or when event generation
			// fails on the final iteration). Account for them instead of
			// silently dropping them from the report.
			flushBatch(ctx, client, cfg, batch, &accepted, &rejected, &errorsCount)
		}(workerID, count)
	}

	wg.Wait()
	return Report{
		Accepted: accepted.Load(),
		Rejected: rejected.Load(),
		Errors:   errorsCount.Load(),
		Duration: time.Since(start),
	}, nil
}

func flushBatch(ctx context.Context, client *http.Client, cfg RunnerConfig, batch [][]byte, accepted, rejected, errorsCount *atomic.Int64) {
	if len(batch) == 0 {
		return
	}
	if err := postBatch(ctx, client, cfg.URL, batch); err != nil {
		var rejection *rejectionError
		if errors.As(err, &rejection) {
			rejected.Add(int64(len(batch)))
		} else {
			errorsCount.Add(int64(len(batch)))
		}
	} else {
		accepted.Add(int64(len(batch)))
	}
}

func postBatch(ctx context.Context, client *http.Client, url string, batch [][]byte) error {
	if !strings.HasSuffix(url, "/events") {
		url = strings.TrimRight(url, "/") + "/events"
	}
	events := make([]json.RawMessage, len(batch))
	for i, b := range batch {
		events[i] = b
	}
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &rejectionError{statusCode: resp.StatusCode}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("loadgen post returned %d", resp.StatusCode)
	}
	return nil
}
