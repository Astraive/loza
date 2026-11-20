package loki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

// Config controls Loki sink behavior.
type Config struct {
	URL string
	// TenantID sets X-Scope-OrgID for multi-tenant Loki deployments.
	TenantID string
	// Labels are attached to each pushed Loki stream.
	Labels map[string]string
	// Headers are added to each Loki push request.
	Headers       map[string]string
	BatchSize     int
	FlushInterval time.Duration
	Timeout       time.Duration
	Client        *http.Client

	// OnFlushError, when set, is invoked with any error returned by a
	// background periodic flush. It is never called for errors returned to
	// callers from WriteEvent, Flush, or Close. Use it to surface repeated
	// push failures to monitoring instead of letting them be silently lost.
	OnFlushError func(error)
}

type sink struct {
	cfg Config

	mu        sync.Mutex
	batch     []lokiEntry
	closed    bool
	stop      chan struct{}
	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup

	// inflight tracks WriteEvent calls that have pulled a batch off s.batch
	// and are about to push it. Close waits on inflight before its final
	// Flush so that a failed push's restore() is included in that Flush
	// instead of being stranded after it.
	inflight sync.WaitGroup
}

type lokiEntry struct {
	Timestamp string
	Line      string
}

type lokiPushPayload struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

var labelKeyPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// New creates a Loki sink.
func New(cfg Config) (collectorevent.Sink, error) {
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.URL == "" {
		return nil, fmt.Errorf("loki: URL is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("loki: invalid URL: %w", err)
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("loki: URL must include scheme and host")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("loki: timeout must be >= 0")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: cfg.Timeout}
	}
	cfg.Labels = normalizedLabels(cfg.Labels)
	if len(cfg.Labels) == 0 {
		cfg.Labels = map[string]string{"source": "loza"}
	}
	cfg.Headers = copyStringMap(cfg.Headers)

	s := &sink{
		cfg:   cfg,
		batch: make([]lokiEntry, 0, cfg.BatchSize),
		stop:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.flushLoop()
	return s, nil
}

func (s *sink) Name() string { return "loki" }

func (s *sink) WriteEvent(ctx context.Context, encoded []byte, ev *collectorevent.Event) error {
	line := strings.TrimSpace(string(encoded))
	if line == "" {
		return fmt.Errorf("loki: empty encoded event")
	}

	ts := time.Now().UTC()
	if ev != nil && !ev.Timestamp.IsZero() {
		ts = ev.Timestamp.UTC()
	}
	entry := lokiEntry{
		Timestamp: fmt.Sprintf("%d", ts.UnixNano()),
		Line:      line,
	}

	batch, err := s.appendEntry(entry)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}
	// appendEntry registered this push while holding s.mu, after confirming
	// the sink was not closed. Pair that registration here so Close can wait
	// for the push (and any restore) to finish before its final Flush.
	defer s.inflight.Done()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.pushBatch(ctx, batch); err != nil {
		s.restore(batch)
		return err
	}
	return nil
}

func (s *sink) Flush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	batch := s.drain()
	if len(batch) == 0 {
		return nil
	}
	if err := s.pushBatch(ctx, batch); err != nil {
		s.restore(batch)
		return err
	}
	return nil
}

func (s *sink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		// Set closed under the same lock that hands out batches for a push.
		// After this point appendEntry cannot register new in-flight pushes,
		// so waiting on inflight below cannot race with a new Add.
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.stop)
		// Drain the periodic loop and every push that was already in flight.
		// A failed in-flight push restores its batch, so finalFlush sees the
		// complete set of entries that still need delivery.
		s.wg.Wait()
		s.inflight.Wait()
		// Store the flush result on the sink so it is published to every
		// Close caller. sync.Once.Do guarantees all callers return only after
		// the winning call finishes, so this write is visible to all of them.
		s.closeErr = s.finalFlush(ctx)
	})
	return s.closeErr
}

// finalFlush performs the one and only flush of a closing sink. It must not
// use Flush: Flush restores a failed batch into s.batch, but after Close the
// flush loop is stopped and appendEntry rejects writes, so restored entries
// would be stranded forever. Instead, a failed final push is reported as an
// explicit drop of the buffered entries.
func (s *sink) finalFlush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	batch := s.drain()
	if len(batch) == 0 {
		return nil
	}
	if err := s.pushBatch(ctx, batch); err != nil {
		return fmt.Errorf("loki: close: dropping %d buffered entries after final flush failed: %w", len(batch), err)
	}
	return nil
}

func (s *sink) flushLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := s.Flush(context.Background()); err != nil {
				s.reportFlushError(err)
			}
		case <-s.stop:
			return
		}
	}
}

// reportFlushError makes a failed periodic flush observable: it always logs
// the error and, when configured, forwards it to the OnFlushError callback so
// callers can wire it into their own monitoring or metrics.
func (s *sink) reportFlushError(err error) {
	if err == nil {
		return
	}
	log.Error().Err(err).Str("sink", s.Name()).Msg("loki: periodic flush failed")
	if s.cfg.OnFlushError != nil {
		s.cfg.OnFlushError(err)
	}
}

func (s *sink) appendEntry(entry lokiEntry) ([]lokiEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("loki: sink closed")
	}
	s.batch = append(s.batch, entry)
	if len(s.batch) < s.cfg.BatchSize {
		return nil, nil
	}
	// Register the in-flight push while still holding s.mu and after the
	// closed check. Close sets closed under the same lock before it waits,
	// so no Add can race with (or follow) that Wait.
	s.inflight.Add(1)
	batch := make([]lokiEntry, len(s.batch))
	copy(batch, s.batch)
	s.batch = s.batch[:0]
	return batch, nil
}

func (s *sink) drain() []lokiEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.batch) == 0 {
		return nil
	}
	batch := make([]lokiEntry, len(s.batch))
	copy(batch, s.batch)
	s.batch = s.batch[:0]
	return batch
}

func (s *sink) restore(batch []lokiEntry) {
	if len(batch) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.batch) > 0 {
		s.batch = append(append(make([]lokiEntry, 0, len(batch)+len(s.batch)), batch...), s.batch...)
	} else {
		s.batch = append(make([]lokiEntry, 0, len(batch)), batch...)
	}
}

func (s *sink) pushBatch(ctx context.Context, batch []lokiEntry) error {
	if len(batch) == 0 {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}

		err := s.doPushBatch(ctx, batch)
		if err == nil {
			return nil
		}
		lastErr = err

		// Check if context is cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("loki: push batch failed after 3 attempts: %w", lastErr)
}

func (s *sink) doPushBatch(ctx context.Context, batch []lokiEntry) error {
	values := make([][]string, 0, len(batch))
	for _, entry := range batch {
		values = append(values, []string{entry.Timestamp, entry.Line})
	}

	payload := lokiPushPayload{
		Streams: []lokiStream{
			{
				Stream: copyStringMap(s.cfg.Labels),
				Values: values,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("loki: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("loki: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(s.cfg.TenantID) != "" {
		req.Header.Set("X-Scope-OrgID", s.cfg.TenantID)
	}
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("loki: push batch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if trimmed := strings.TrimSpace(string(msg)); trimmed != "" {
			return fmt.Errorf("loki: unexpected status %d: %s", resp.StatusCode, trimmed)
		}
		return fmt.Errorf("loki: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func normalizedLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		key := strings.TrimSpace(k)
		if !labelKeyPattern.MatchString(key) {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	return out
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
