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

func Run(ctx context.Context, cfg RunnerConfig) (Report, error) {
	if cfg.Workers <= 0 {
		return Report{}, fmt.Errorf("loadgen: workers must be greater than zero, got %d", cfg.Workers)
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
					batch = batch[:0]
				}
			}
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
