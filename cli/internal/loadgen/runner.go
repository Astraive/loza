package loadgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxAcknowledgementBytes = 1 << 20

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
	outcome, err := postBatch(ctx, client, cfg.URL, batch)
	if err != nil {
		var rejection *rejectionError
		if errors.As(err, &rejection) {
			rejected.Add(int64(len(batch)))
		} else {
			errorsCount.Add(int64(len(batch)))
		}
		return
	}
	accepted.Add(int64(outcome.accepted))
	rejected.Add(int64(outcome.rejected))
}

type batchOutcome struct {
	accepted int
	rejected int
}

func postBatch(ctx context.Context, client *http.Client, url string, batch [][]byte) (batchOutcome, error) {
	if !strings.HasSuffix(url, "/events") {
		url = strings.TrimRight(url, "/") + "/events"
	}
	events := make([]json.RawMessage, len(batch))
	for i, b := range batch {
		events[i] = b
	}
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return batchOutcome{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return batchOutcome{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return batchOutcome{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return batchOutcome{}, &rejectionError{statusCode: resp.StatusCode}
	}
	if resp.StatusCode >= 300 {
		return batchOutcome{}, fmt.Errorf("loadgen post returned %d", resp.StatusCode)
	}

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAcknowledgementBytes+1))
	if err != nil {
		return batchOutcome{}, fmt.Errorf("read Collector acknowledgement: %w", err)
	}
	if len(responseBody) > maxAcknowledgementBytes {
		return batchOutcome{}, fmt.Errorf("Collector acknowledgement exceeds %d bytes", maxAcknowledgementBytes)
	}
	outcome, err := parseAcknowledgement(responseBody, len(batch))
	if err != nil {
		return batchOutcome{}, err
	}
	return outcome, nil
}

// parseAcknowledgement accepts the Collector's status/count acknowledgement
// fields. Invalid and quarantined events are reported as rejected because the
// loadgen report has no separate counters for those non-accepted outcomes.
func parseAcknowledgement(body []byte, batchSize int) (batchOutcome, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement JSON: %w", err)
	}
	if fields == nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: expected an object")
	}

	var requestID string
	if raw, ok := fields["request_id"]; !ok || json.Unmarshal(raw, &requestID) != nil || !strings.HasPrefix(requestID, "ing_") {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: missing or invalid request_id")
	}
	var status string
	if raw, ok := fields["status"]; !ok || json.Unmarshal(raw, &status) != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: missing or invalid status")
	}
	switch status {
	case "accepted", "partial", "rejected", "invalid", "quarantined":
	default:
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: unsupported status %q", status)
	}

	readCount := func(name string, required bool) (int, error) {
		raw, ok := fields[name]
		if !ok {
			if required {
				return 0, fmt.Errorf("missing %s count", name)
			}
			return 0, nil
		}
		var value *int
		if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value < 0 {
			return 0, fmt.Errorf("invalid %s count", name)
		}
		return *value, nil
	}
	accepted, err := readCount("accepted", true)
	if err != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: %w", err)
	}
	rejected, err := readCount("rejected", true)
	if err != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: %w", err)
	}
	invalid, err := readCount("invalid", false)
	if err != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: %w", err)
	}
	quarantined, err := readCount("quarantined", false)
	if err != nil {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: %w", err)
	}
	if accepted > batchSize || rejected > batchSize-accepted {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: counts exceed batch size %d", batchSize)
	}
	remaining := batchSize - accepted - rejected
	if invalid > remaining {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: counts exceed batch size %d", batchSize)
	}
	remaining -= invalid
	if quarantined > remaining {
		return batchOutcome{}, fmt.Errorf("invalid Collector acknowledgement: counts exceed batch size %d", batchSize)
	}
	remaining -= quarantined
	if remaining != 0 {
		return batchOutcome{}, fmt.Errorf(
			"invalid Collector acknowledgement: counts accepted=%d rejected=%d invalid=%d quarantined=%d do not match batch size %d",
			accepted, rejected, invalid, quarantined, batchSize,
		)
	}

	reportedRejected := batchSize - accepted
	expectedStatus := "rejected"
	switch {
	case accepted > 0 && reportedRejected > 0:
		expectedStatus = "partial"
	case accepted > 0:
		expectedStatus = "accepted"
	case quarantined > 0:
		expectedStatus = "quarantined"
	case invalid > 0:
		expectedStatus = "invalid"
	}
	if status != expectedStatus {
		return batchOutcome{}, fmt.Errorf(
			"invalid Collector acknowledgement: status %q contradicts event counts (expected %q)",
			status, expectedStatus,
		)
	}
	return batchOutcome{accepted: accepted, rejected: reportedRejected}, nil
}
