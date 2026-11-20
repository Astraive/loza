package httpbatch

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/astraive/loza/sdks/go"
)

// maxBufferedFactor bounds the retained buffer to a small multiple of the
// configured batch size. Without a cap, a failed batch is folded back into the
// buffer together with every event written during the send, so sustained send
// failures grow memory in proportion to the incoming event rate.
const maxBufferedFactor = 2

// Config controls HTTP batch sink behavior.
type Config struct {
	URL           string
	Method        string
	Headers       map[string]string
	BatchSize     int
	FlushInterval time.Duration
	// MaxBufferSize caps the number of retained events. When the buffer is
	// full, the oldest events are dropped so a failing collector cannot make
	// the buffer grow without bound. Default: 10000.
	MaxBufferSize int
	Gzip          bool
	Client        *http.Client
	OnError       func(error)
	// OnDrop is notified once for every event discarded by the overflow policy.
	OnDrop func(reason string)
}

type sink struct {
	cfg          Config
	mu           sync.Mutex
	flushMu      sync.Mutex
	buf          [][]byte
	droppedCount int64 // guarded by mu
	timer        *time.Ticker
	stop         chan struct{}
	closeOnce    sync.Once
}

// New creates an HTTP batch sink.
func New(cfg Config) (loza.Sink, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("httpbatch: URL is required")
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if cfg.MaxBufferSize <= 0 {
		cfg.MaxBufferSize = 10000
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}

	s := &sink{
		cfg:   cfg,
		buf:   make([][]byte, 0, cfg.BatchSize),
		timer: time.NewTicker(cfg.FlushInterval),
		stop:  make(chan struct{}),
	}
	go s.loop()
	return s, nil
}

func (s *sink) Name() string { return "httpbatch" }

// maxBuffered returns the hard cap on the number of buffered events. New
// normalizes BatchSize to a positive value, but keep a defensive floor in case
// a sink is constructed directly in tests.
func (s *sink) maxBuffered() int {
	if s.cfg.BatchSize <= 0 {
		return maxBufferedFactor
	}
	return s.cfg.BatchSize * maxBufferedFactor
}

// trimBuf drops the oldest buffered events when the buffer exceeds its cap and
// returns the number of events dropped. Callers must hold s.mu.
func (s *sink) trimBuf() int {
	max := s.maxBuffered()
	if len(s.buf) <= max {
		return 0
	}
	dropped := len(s.buf) - max
	copy(s.buf, s.buf[dropped:])
	for i := len(s.buf) - dropped; i < len(s.buf); i++ {
		s.buf[i] = nil
	}
	s.buf = s.buf[:max]
	return dropped
}

// retainFailed puts a failed batch back ahead of events buffered during the
// send and applies the cap, dropping the oldest events if needed. Callers must
// hold s.mu.
func (s *sink) retainFailed(batch [][]byte) int {
	s.buf = append(batch, s.buf...)
	return s.trimBuf()
}

func (s *sink) notifyDropped(dropped int) {
	if dropped > 0 && s.cfg.OnError != nil {
		s.cfg.OnError(fmt.Errorf("httpbatch: buffer full, dropped %d events", dropped))
	}
}

// enforceBufferLimitLocked drops the oldest buffered events until the buffer
// fits MaxBufferSize. The caller must hold s.mu. It returns the number dropped.
func (s *sink) enforceBufferLimitLocked() int {
	dropped := 0
	for len(s.buf) > s.cfg.MaxBufferSize {
		s.buf[0] = nil // release the retained bytes for GC
		s.buf = s.buf[1:]
		dropped++
	}
	s.droppedCount += int64(dropped)
	return dropped
}

func (s *sink) notifyDrops(reason string, count int) {
	if s.cfg.OnDrop == nil {
		return
	}
	for range count {
		s.cfg.OnDrop(reason)
	}
}

func (s *sink) WriteEvent(ctx context.Context, encoded []byte, _ *loza.Event) error {
	s.mu.Lock()
	cp := make([]byte, len(encoded))
	copy(cp, encoded)
	s.buf = append(s.buf, cp)
	dropped := s.enforceBufferLimitLocked()
	flush := len(s.buf) >= s.cfg.BatchSize
	s.mu.Unlock()
	s.notifyDrops("buffer_overflow_drop_oldest", dropped)
	if flush {
		// Propagate the caller's context so cancellation aborts the write.
		return s.Flush(ctx)
	}
	return nil
}

// Size returns the number of events currently buffered.
func (s *sink) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

// DroppedCount returns the number of events dropped by the overflow policy.
func (s *sink) DroppedCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.droppedCount
}

func (s *sink) WriteBatch(ctx context.Context, encoded [][]byte, _ []*loza.Event) error {
	if len(encoded) == 0 {
		return nil
	}
	// Route the batch through the buffered flush path so it can never overtake
	// events retained in s.buf after a failed flush. Appending each item keeps
	// the byte-for-byte concatenation of bytes.Join(encoded, nil) while
	// preserving the order of any older, already-retained events.
	s.mu.Lock()
	for _, e := range encoded {
		cp := make([]byte, len(e))
		copy(cp, e)
		s.buf = append(s.buf, cp)
	}
	s.mu.Unlock()
	return s.Flush(ctx)
}

func (s *sink) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		s.flushMu.Unlock()
		return nil
	}
	batch := s.buf
	s.buf = make([][]byte, 0, s.cfg.BatchSize)
	s.mu.Unlock()

	err := s.send(ctx, bytes.Join(batch, nil))
	var dropped int
	if err != nil {
		s.mu.Lock()
		dropped = s.retainFailed(batch)
		s.mu.Unlock()
		s.flushMu.Unlock()
		s.notifyDropped(dropped)
		return err
	}
	s.flushMu.Unlock()

	// Notify after releasing flushMu so an OnError callback may call Flush.
	s.notifyDropped(dropped)
	return err
}

func (s *sink) send(ctx context.Context, payload []byte) error {
	body := payload
	if s.cfg.Gzip {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		if _, err := zw.Write(payload); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		body = b.Bytes()
	}

	req, err := http.NewRequestWithContext(ctx, s.cfg.Method, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.cfg.Gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// Drain response body to allow HTTP keep-alive reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("httpbatch: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (s *sink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.timer.Stop()
	})
	return s.Flush(ctx)
}

func (s *sink) loop() {
	for {
		select {
		case <-s.timer.C:
			if err := s.Flush(context.Background()); err != nil && s.cfg.OnError != nil {
				s.cfg.OnError(err)
			}
		case <-s.stop:
			return
		}
	}
}
