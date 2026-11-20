package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	collectorevent "github.com/astraive/loza/collector/internal/event"
	"github.com/astraive/loza/collector/internal/sinks/internal/atrest"
	"github.com/astraive/loza/collector/internal/sinks/internal/projection"
	storagepath "github.com/astraive/loza/collector/internal/storage"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config controls S3 sink behavior.
type Config struct {
	Client      *s3.Client
	Bucket      string
	Prefix      string
	ObjectKeyFn func() string
	// Schema projects output payload keys from encoded events.
	Schema        map[string]string
	BatchSize     int
	FlushInterval time.Duration
	// FlushTimeout bounds each ticker-driven flush so a stalled S3 call cannot
	// pin the flush loop (and, transitively, Close) indefinitely. Optional;
	// defaults to 30s when unset.
	FlushTimeout time.Duration
	EncryptKey   string
}

type sink struct {
	cfg Config
	// mu protects buf, count and closed.
	mu sync.Mutex
	// uploadMu serializes a whole drain+upload+restore cycle so that a
	// single flush owns the buffer at a time. Without it, a WriteEvent
	// batch and a ticker/Flush batch could upload concurrently: on a
	// partial failure restore() would prepend records ahead of entries
	// appended in the meantime, reordering or duplicating NDJSON records.
	uploadMu  sync.Mutex
	buf       bytes.Buffer
	batchSize int
	columns   []string
	count     int
	closed    bool
	stop      chan struct{}
	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup
	// inflight tracks every putObject that may restore records to the buffer.
	// Close waits for it so restored records are included in the final flush.
	inflight    sync.WaitGroup
	flushCtx    context.Context
	cancelFlush context.CancelFunc
}

// New creates an S3 sink.
func New(cfg Config) (collectorevent.Sink, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("s3: Client is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: Bucket is required")
	}
	if cfg.ObjectKeyFn == nil {
		prefix := cfg.Prefix
		cfg.ObjectKeyFn = func() string {
			return storagepath.NDJSONArchiveKey(prefix, time.Now().UTC())
		}
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.FlushTimeout <= 0 {
		cfg.FlushTimeout = 30 * time.Second
	}

	flushCtx, cancelFlush := context.WithCancel(context.Background())
	s := &sink{
		cfg:         cfg,
		batchSize:   cfg.BatchSize,
		stop:        make(chan struct{}),
		flushCtx:    flushCtx,
		cancelFlush: cancelFlush,
	}
	if len(cfg.Schema) > 0 {
		s.cfg.Schema = make(map[string]string, len(cfg.Schema))
		for col, path := range cfg.Schema {
			col = strings.TrimSpace(col)
			if col == "" {
				return nil, fmt.Errorf("s3: schema column cannot be empty")
			}
			path = strings.TrimSpace(path)
			if path == "" {
				return nil, fmt.Errorf("s3: empty schema path for column %q", col)
			}
			if err := projection.ValidatePath(path); err != nil {
				return nil, fmt.Errorf("s3: invalid schema path for column %q: %w", col, err)
			}
			s.cfg.Schema[col] = path
		}
		s.columns = projection.SortedColumns(s.cfg.Schema)
	}
	s.wg.Add(1)
	go s.flushLoop()
	return s, nil
}

func (s *sink) Name() string { return "s3" }

func (s *sink) WriteEvent(ctx context.Context, encoded []byte, _ *collectorevent.Event) error {
	if len(s.columns) > 0 {
		var err error
		encoded, err = projection.ProjectEncoded(encoded, s.cfg.Schema, s.columns)
		if err != nil {
			return err
		}
	}
	// Hold uploadMu across appendLocked as well: appendLocked drains a full
	// batch, so the drain must stay under the same lock as the upload that
	// follows it. This keeps concurrent flushes from owning the buffer at
	// the same time.
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	payload, n, err := s.appendLocked(encoded)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return s.putTracked(ctx, payload, n)
}

func (s *sink) Flush(ctx context.Context) error { return s.flushBatch(ctx) }

func (s *sink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.stop)
		// Cancel any in-flight ticker-driven flush so the loop goroutine can
		// observe s.stop and let wg.Wait() return promptly. The payload is
		// restored by flushBatch and re-sent by the final flush below.
		if s.cancelFlush != nil {
			s.cancelFlush()
		}
		s.wg.Wait()
		// An in-flight put may still fail and restore its payload after the
		// flush loop has stopped. Wait for all of them before the final flush
		// so restored records are uploaded (or surface as a Close error).
		s.inflight.Wait()
		// finalFlush is the one and only final drain. Running flushBatch
		// afterwards would find the restored payload (when finalFlush fails),
		// see s.closed, and overwrite the real error with "s3: sink closed".
		s.closeErr = s.finalFlush(ctx)
	})
	return s.closeErr
}

func (s *sink) flushLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			// Bound every periodic flush with a deadline, and derive it from
			// flushCtx so Close() can cancel it immediately via cancelFlush.
			parent := s.flushCtx
			if parent == nil {
				parent = context.Background()
			}
			flushCtx, cancel := context.WithTimeout(parent, s.cfg.FlushTimeout)
			_ = s.flushBatch(flushCtx)
			cancel()
		case <-s.stop:
			return
		}
	}
}

func (s *sink) appendLocked(encoded []byte) ([]byte, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0, errors.New("s3: sink closed")
	}
	s.buf.Write(encoded)
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		s.buf.WriteByte('\n')
	}
	s.count++
	if s.count < s.batchSize {
		return nil, 0, nil
	}
	payload := append([]byte(nil), s.buf.Bytes()...)
	n := s.count
	s.buf.Reset()
	s.count = 0
	return payload, n, nil
}

func (s *sink) flushBatch(ctx context.Context) error {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	payload, n := s.drain()
	if len(payload) == 0 {
		return nil
	}
	return s.putTracked(ctx, payload, n)
}

// putTracked registers a putObject call with inflight so Close can wait for
// it. Add and the closed check share s.mu: once Close sets closed, no new put
// can be registered, which keeps Add strictly before Close's Wait.
func (s *sink) putTracked(ctx context.Context, payload []byte, n int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.restore(payload, n)
		return errors.New("s3: sink closed")
	}
	s.inflight.Add(1)
	s.mu.Unlock()
	defer s.inflight.Done()
	return s.putAndRestore(ctx, payload, n)
}

// finalFlush is the untracked final drain used by Close after inflight.Wait
// has returned; no other put can be in flight at that point.
func (s *sink) finalFlush(ctx context.Context) error {
	payload, n := s.drain()
	if len(payload) == 0 {
		return nil
	}
	return s.putAndRestore(ctx, payload, n)
}

func (s *sink) putAndRestore(ctx context.Context, payload []byte, n int) error {
	if err := s.putObject(ctx, payload); err != nil {
		s.restore(payload, n)
		return err
	}
	return nil
}

func (s *sink) drain() ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return nil, 0
	}
	payload := append([]byte(nil), s.buf.Bytes()...)
	n := s.count
	s.buf.Reset()
	s.count = 0
	return payload, n
}

func (s *sink) restore(payload []byte, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf.Bytes()) > 0 {
		existing := append([]byte(nil), s.buf.Bytes()...)
		s.buf.Reset()
		s.buf.Write(payload)
		s.buf.Write(existing)
	} else {
		s.buf.Reset()
		s.buf.Write(payload)
	}
	s.count += n
}

func (s *sink) putObject(ctx context.Context, payload []byte) error {
	key := s.cfg.ObjectKeyFn()
	if strings.TrimSpace(s.cfg.EncryptKey) != "" {
		var err error
		payload, err = atrest.EncryptBytes(payload, s.cfg.EncryptKey)
		if err != nil {
			return err
		}
	}
	_, err := s.cfg.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.cfg.Bucket,
		Key:         &key,
		Body:        bytes.NewReader(payload),
		ContentType: strPtr("application/x-ndjson"),
	})
	return err
}

func strPtr(v string) *string { return &v }
