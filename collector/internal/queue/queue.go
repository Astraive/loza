package queue

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	ErrQueueFull       = errors.New("queue is full, applying backpressure")
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrQueueClosed     = errors.New("queue is closed")
	ErrSinkUnavailable = errors.New("sink is currently unavailable")
)

type Event struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   []byte         `json:"payload"`
	Attempts  int            `json:"attempts"`
	Metadata  map[string]any `json:"metadata,omitempty"`

	// nextAttempt is the earliest time this event may be retried after a
	// failed flush. It is internal state and is deliberately not serialized.
	nextAttempt time.Time
}

type QueueConfig struct {
	Directory               string
	MaxSizeBytes            int64
	MaxEvents               int
	BatchSize               int
	BatchTimeout            time.Duration
	FlushInterval           time.Duration
	Sink                    Sink
	SinkName                string
	RetryEnabled            bool
	RetryMaxAttempts        int
	RetryInitialBackoff     time.Duration
	RetryMaxBackoff         time.Duration
	RetryJitter             bool
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   time.Duration
	CircuitBreakerHalfOpen  bool
}

type Sink interface {
	WriteEvent(ctx context.Context, data []byte, key []byte) error
	WriteBatch(ctx context.Context, events [][]byte) error
	Flush(ctx context.Context) error
	Close(ctx context.Context) error
}

type State struct {
	Enqueued      int64 `json:"enqueued"`
	Flushed       int64 `json:"flushed"`
	Failed        int64 `json:"failed"`
	Retried       int64 `json:"retried"`
	Backpressured int64 `json:"backpressured"`
	InFlight      int64 `json:"in_flight"`
	CircuitOpen   bool  `json:"circuit_open"`
}

type Queue struct {
	cfg    QueueConfig
	events []Event
	// queueBytes is the cumulative payload size of the buffered events in
	// q.events. It is guarded by mu and kept in sync with q.events so that
	// MaxSizeBytes backpressure can be enforced alongside MaxEvents.
	queueBytes int64
	mu         sync.Mutex
	cond       *sync.Cond
	closed     bool
	cfg           QueueConfig
	events        []Event
	oldestEventAt time.Time
	mu            sync.Mutex
	wakeCh        chan struct{}
	closed        bool

	inflight   map[string]Event
	inflightMu sync.Mutex

	circuitFailures int
	circuitOpenTime time.Time
	halfOpenTrial   bool
	circuitMu       sync.Mutex

	state   State
	stateMu sync.RWMutex

	stopCh    chan struct{}
	flushDone chan struct{}
	wg        sync.WaitGroup
	rng       *rand.Rand
	rngMu     sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

func NewQueue(cfg QueueConfig) (*Queue, error) {
	if cfg.Directory == "" {
		return nil, errors.New("queue directory is required")
	}
	if cfg.Sink == nil {
		return nil, errors.New("sink is required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 5 * time.Second
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 10 * time.Second
	}
	if cfg.RetryMaxAttempts <= 0 {
		cfg.RetryMaxAttempts = 3
	}
	if cfg.RetryInitialBackoff <= 0 {
		cfg.RetryInitialBackoff = 100 * time.Millisecond
	}
	if cfg.RetryMaxBackoff <= 0 {
		cfg.RetryMaxBackoff = 10 * time.Second
	}
	if cfg.CircuitBreakerThreshold <= 0 {
		cfg.CircuitBreakerThreshold = 10
	}
	if cfg.CircuitBreakerTimeout <= 0 {
		cfg.CircuitBreakerTimeout = 30 * time.Second
	}

	if err := os.MkdirAll(cfg.Directory, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir queue dir: %w", err)
	}

	q := &Queue{
		cfg:       cfg,
		events:    make([]Event, 0, cfg.BatchSize),
		inflight:  make(map[string]Event),
		wakeCh:    make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
		flushDone: make(chan struct{}, 1),
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	q.wg.Add(1)
	go q.flushLoop()

	return q, nil
}

// exceedsSizeLimit reports whether buffering an event with the given payload
// size would push the queue past its MaxSizeBytes limit. A non-positive limit
// disables byte-based backpressure. Callers must hold q.mu.
func (q *Queue) exceedsSizeLimit(payloadBytes int64) bool {
	if q.cfg.MaxSizeBytes <= 0 {
		return false
	}
	// queueBytes never exceeds MaxSizeBytes (additions that would do so are
	// rejected), so this subtraction cannot underflow.
	return payloadBytes > q.cfg.MaxSizeBytes-q.queueBytes
}

func (q *Queue) Enqueue(ctx context.Context, event Event) error {
	if event.ID == "" {
		event.ID = q.generateEventID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	if q.isCircuitOpen() {
		q.stateMu.Lock()
		q.state.Backpressured++
		q.stateMu.Unlock()
		return ErrCircuitOpen
	}

	if q.cfg.MaxEvents > 0 && len(q.events) >= q.cfg.MaxEvents {
		q.stateMu.Lock()
		q.state.Backpressured++
		q.stateMu.Unlock()
		return ErrQueueFull
	}

	payloadBytes := int64(len(event.Payload))
	if q.exceedsSizeLimit(payloadBytes) {
		q.stateMu.Lock()
		q.state.Backpressured++
		q.stateMu.Unlock()
		return ErrQueueFull
	}

	if len(q.events) == 0 {
		q.oldestEventAt = time.Now()
	}
	q.events = append(q.events, event)
	q.queueBytes += payloadBytes
	q.stateMu.Lock()
	q.state.Enqueued++
	q.state.InFlight++
	q.stateMu.Unlock()

	q.inflightMu.Lock()
	q.inflight[event.ID] = event
	q.inflightMu.Unlock()

	q.notifyFlush()

	return nil
}

func (q *Queue) EnqueueBatch(ctx context.Context, events []Event) []error {
	q.mu.Lock()
	defer q.mu.Unlock()

	errors := make([]error, len(events))

	if q.closed {
		for i := range events {
			errors[i] = ErrQueueClosed
		}
		return errors
	}

	if q.isCircuitOpen() {
		for i := range events {
			errors[i] = ErrCircuitOpen
		}
		q.stateMu.Lock()
		q.state.Backpressured += int64(len(events))
		q.stateMu.Unlock()
		return errors
	}

	for i, event := range events {
		if event.ID == "" {
			event.ID = q.generateEventID()
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now()
		}

		if q.cfg.MaxEvents > 0 && len(q.events) >= q.cfg.MaxEvents {
			errors[i] = ErrQueueFull
			q.stateMu.Lock()
			q.state.Backpressured++
			q.stateMu.Unlock()
			continue
		}

		payloadBytes := int64(len(event.Payload))
		if q.exceedsSizeLimit(payloadBytes) {
			errors[i] = ErrQueueFull
			q.stateMu.Lock()
			q.state.Backpressured++
			q.stateMu.Unlock()
			continue
		}

		if len(q.events) == 0 {
			q.oldestEventAt = time.Now()
		}
		q.events = append(q.events, event)
		q.queueBytes += payloadBytes
		errors[i] = nil

		q.inflightMu.Lock()
		q.inflight[event.ID] = event
		q.inflightMu.Unlock()
	}

	enqueued := 0
	for _, err := range errors {
		if err == nil {
			enqueued++
		}
	}

	q.stateMu.Lock()
	q.state.Enqueued += int64(enqueued)
	q.state.InFlight += int64(enqueued)
	q.stateMu.Unlock()

	q.notifyFlush()

	return errors
}

func (q *Queue) flushLoop() {
	defer q.wg.Done()

	ticker := time.NewTicker(q.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		q.mu.Lock()
		if len(q.events) == 0 {
			q.mu.Unlock()
			select {
			case <-q.stopCh:
				q.markClosed()
				return
			case <-ticker.C:
			case <-q.wakeCh:
			}
			continue
		}

		// Flush a partial batch once its oldest event has waited BatchTimeout.
		wait := q.cfg.BatchTimeout - time.Since(q.oldestEventAt)
		if wait < 0 {
			wait = 0
		}
		q.mu.Unlock()

		timer := time.NewTimer(wait)
		var timedFlush bool
		select {
		case <-q.stopCh:
			stopTimer(timer)
			q.markClosed()
			return
		case <-ticker.C:
			q.mu.Lock()
			if len(q.events) > 0 && q.hasReadyEvent() {
				q.mu.Unlock()
				if err := q.doFlush(); err != nil {
					log.Printf("queue flush error: %v", err)
				}
			} else {
				q.mu.Unlock()
			timedFlush = true
		case <-q.wakeCh:
			// A new event arrived; recompute the deadline for the oldest one.
		case <-timer.C:
			timedFlush = true
		}
		stopTimer(timer)

		q.mu.Lock()
		overBatchSize := len(q.events) >= q.cfg.BatchSize
		q.mu.Unlock()

		if timedFlush || overBatchSize {
			if err := q.doFlush(); err != nil {
				log.Printf("queue flush error: %v", err)
			}
		}
	}
}

// notifyFlush wakes the flush loop after an event is buffered. The wake
// channel is buffered with capacity one so notifications sent while the loop
// is busy are coalesced rather than lost.
func (q *Queue) notifyFlush() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}

func (q *Queue) markClosed() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
			q.mu.Lock()
			if q.hasReadyEvent() && (len(q.events) >= q.cfg.BatchSize || q.hasReadyRetry()) {
			// Close() broadcasts the cond while holding q.mu. Recheck stopCh
			// under that same lock so a wake-up delivered before we parked in
			// cond.Wait() cannot be missed.
			select {
			case <-q.stopCh:
				q.mu.Unlock()
				return
			default:
			}
			if len(q.events) >= q.cfg.BatchSize {
				q.mu.Unlock()
				if err := q.doFlush(); err != nil {
					log.Printf("queue flush error: %v", err)
				}
				continue
			}
			q.cond.Wait()
			q.mu.Unlock()
		}
	}
}

func (q *Queue) doFlush() error {
	q.mu.Lock()
	if len(q.events) == 0 {
		q.mu.Unlock()
		return nil
	}

	// A failed event carries a retry deadline; keep it queued but skip it
	// until its backoff has elapsed. Only ready events go into this batch.
	now := time.Now()
	events := make([]Event, 0, len(q.events))
	deferred := make([]Event, 0)
	for _, e := range q.events {
		if e.nextAttempt.After(now) {
			deferred = append(deferred, e)
		} else {
			events = append(events, e)
		}
	}
	if len(events) == 0 {
		q.mu.Unlock()
		return nil
	}
	q.events = deferred
	// Only the ready events leave the queue; deferred events remain buffered,
	// so subtract just the bytes of the events taken into this batch to keep
	// queueBytes in sync with q.events for MaxSizeBytes backpressure.
	for _, e := range events {
		q.queueBytes -= int64(len(e.Payload))
	}
	events := make([]Event, len(q.events))
	copy(events, q.events)
	q.events = q.events[:0]
	q.oldestEventAt = time.Time{}
	q.mu.Unlock()

	batch := make([][]byte, len(events))
	for i, e := range events {
		batch[i] = e.Payload
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var flushErr error
	err := q.cfg.Sink.WriteBatch(ctx, batch)
	if err != nil {
		flushErr = fmt.Errorf("write batch failed: %w", err)
		q.recordFailure()
	}

	if flushErr == nil {
		q.recordSuccess()
	}

	// Collect retry events under stateMu, then re-acquire q.mu to append.
	var retryEvents []Event

	q.inflightMu.Lock()
	q.stateMu.Lock()

	if flushErr == nil {
		for _, e := range events {
			delete(q.inflight, e.ID)
		}
		q.state.Flushed += int64(len(events))
		q.state.InFlight -= int64(len(events))
	} else {
		if q.cfg.RetryEnabled {
			retryEvents = make([]Event, 0, len(events))
			for _, e := range events {
				e.Attempts++
				if e.Attempts < q.cfg.RetryMaxAttempts {
					// Wait out the configured exponential backoff before this
					// event is eligible for another delivery attempt.
					e.nextAttempt = time.Now().Add(q.retryBackoff(e.Attempts))
					retryEvents = append(retryEvents, e)
					// Don't decrement InFlight — retried events stay in flight.
					delete(q.inflight, e.ID)
					q.state.Retried++
				} else {
					delete(q.inflight, e.ID)
					q.state.InFlight--
					q.state.Failed++
				}
			}
		} else {
			q.state.InFlight -= int64(len(events))
			q.state.Failed += int64(len(events))
		}
	}

	q.stateMu.Unlock()
	q.inflightMu.Unlock()

	// Re-queue retry events under q.mu (the lock that protects q.events),
	// then arrange a wake-up for the earliest pending retry deadline instead
	// of retrying immediately.
	if len(retryEvents) > 0 {
		q.mu.Lock()
		if len(q.events) == 0 {
			q.oldestEventAt = time.Now()
		}
		q.events = append(q.events, retryEvents...)
		for _, e := range retryEvents {
			q.queueBytes += int64(len(e.Payload))
		}
		earliest := q.earliestRetry()
		q.mu.Unlock()
		if !earliest.IsZero() {
			time.AfterFunc(time.Until(earliest), func() {
				q.mu.Lock()
				q.cond.Signal()
				q.mu.Unlock()
			})
		}
		q.notifyFlush()
	}

	// Only flush if WriteBatch succeeded — don't mask the original error.
	if flushErr == nil {
		if err := q.cfg.Sink.Flush(ctx); err != nil {
			return fmt.Errorf("flush failed: %w", err)
		}
	}
	return flushErr
}

// retryBackoff returns the delay before the next delivery attempt for an
// event that has just failed its attempt-th try. It doubles the configured
// initial backoff for each attempt, caps it at RetryMaxBackoff, and applies
// multiplicative jitter in the [0.8, 1.2) range when RetryJitter is enabled
// (mirroring the sibling processing processor).
func (q *Queue) retryBackoff(attempt int) time.Duration {
	backoff := q.cfg.RetryInitialBackoff
	if backoff <= 0 {
		return 0
	}
	for i := 1; i < attempt; i++ {
		if q.cfg.RetryMaxBackoff > 0 && backoff >= q.cfg.RetryMaxBackoff {
			break
		}
		backoff *= 2
	}
	if q.cfg.RetryMaxBackoff > 0 && backoff > q.cfg.RetryMaxBackoff {
		backoff = q.cfg.RetryMaxBackoff
	}
	if q.cfg.RetryJitter {
		q.rngMu.Lock()
		factor := 0.8 + 0.4*q.rng.Float64()
		q.rngMu.Unlock()
		backoff = time.Duration(float64(backoff) * factor)
	}
	return backoff
}

// hasReadyEvent reports whether any queued event has passed its retry
// deadline. The caller must hold q.mu.
func (q *Queue) hasReadyEvent() bool {
	now := time.Now()
	for _, e := range q.events {
		if !e.nextAttempt.After(now) {
			return true
		}
	}
	return false
}

// hasReadyRetry reports whether a previously failed event has reached its
// retry deadline and is ready to be re-attempted. Fresh events (with no
// deadline) are excluded so normal batching behavior is preserved. The caller
// must hold q.mu.
func (q *Queue) hasReadyRetry() bool {
	now := time.Now()
	for _, e := range q.events {
		if !e.nextAttempt.IsZero() && !e.nextAttempt.After(now) {
			return true
		}
	}
	return false
}

// earliestRetry returns the soonest future retry deadline among queued
// events, or the zero time if there are none. The caller must hold q.mu.
func (q *Queue) earliestRetry() time.Time {
	now := time.Now()
	var earliest time.Time
	for _, e := range q.events {
		if e.nextAttempt.After(now) && (earliest.IsZero() || e.nextAttempt.Before(earliest)) {
			earliest = e.nextAttempt
		}
	}
	return earliest
}

func (q *Queue) recordFailure() {
	q.circuitMu.Lock()
	defer q.circuitMu.Unlock()

	if q.halfOpenTrial {
		// The trial request failed: reopen the circuit and restart the
		// cooldown rather than sending the sink the full stream again.
		q.halfOpenTrial = false
		q.circuitOpenTime = time.Now()
		q.circuitFailures = q.cfg.CircuitBreakerThreshold
		q.stateMu.Lock()
		q.state.CircuitOpen = true
		q.stateMu.Unlock()
		return
	}

	q.circuitFailures++
	if q.circuitFailures >= q.cfg.CircuitBreakerThreshold {
		q.circuitOpenTime = time.Now()
		q.stateMu.Lock()
		q.state.CircuitOpen = true
		q.stateMu.Unlock()
	}
}

func (q *Queue) recordSuccess() {
	q.circuitMu.Lock()
	defer q.circuitMu.Unlock()

	if q.halfOpenTrial {
		// The trial request succeeded: the sink has recovered, so close the
		// circuit and let traffic flow normally again.
		q.halfOpenTrial = false
		q.circuitOpenTime = time.Time{}
		q.circuitFailures = 0
		q.stateMu.Lock()
		q.state.CircuitOpen = false
		q.stateMu.Unlock()
		return
	}

	if q.circuitFailures > 0 {
		q.circuitFailures--
		if q.circuitFailures == 0 {
			q.stateMu.Lock()
			if q.state.CircuitOpen && time.Since(q.circuitOpenTime) > q.cfg.CircuitBreakerTimeout {
				q.state.CircuitOpen = false
			}
			q.stateMu.Unlock()
		}
	}
}

func (q *Queue) isCircuitOpen() bool {
	q.circuitMu.Lock()
	defer q.circuitMu.Unlock()

	if q.circuitOpenTime.IsZero() {
		return false
	}

	if time.Since(q.circuitOpenTime) <= q.cfg.CircuitBreakerTimeout {
		return true
	}

	// Cooldown elapsed. With half-open enabled, admit exactly one trial
	// request; everyone else stays rejected until the trial's outcome is
	// recorded by recordSuccess or recordFailure.
	if q.cfg.CircuitBreakerHalfOpen {
		if q.halfOpenTrial {
			return true
		}
		q.halfOpenTrial = true
		return false
	}

	q.circuitOpenTime = time.Time{}
	q.circuitFailures = 0
	q.stateMu.Lock()
	q.state.CircuitOpen = false
	q.stateMu.Unlock()
	return false
}

func (q *Queue) State() State {
	q.stateMu.RLock()
	defer q.stateMu.RUnlock()
	return q.state
}

func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		// Signal shutdown while holding q.mu so flushLoop cannot park in
		// cond.Wait() after the broadcast and miss the wake-up entirely.
		q.mu.Lock()
		close(q.stopCh)
		q.cond.Broadcast()
		q.mu.Unlock()

		q.wg.Wait()

		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()

		// Final flush of any remaining buffered events
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := q.doFlush(); err != nil {
			q.closeErr = err
			return
		}

		q.closeErr = q.cfg.Sink.Close(ctx)
	})
	return q.closeErr
}

func (q *Queue) generateEventID() string {
	q.rngMu.Lock()
	id := fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomString(q.rng, 16))
	q.rngMu.Unlock()
	return id
}

func randomString(rng *rand.Rand, n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

type DiskSinkConfig struct {
	Path        string
	MaxFileSize int64
	MaxFiles    int
	EncryptKey  string
}

type DiskSink struct {
	cfg  DiskSinkConfig
	file *os.File
	mu   sync.Mutex
	pos  int64
}

func NewDiskSink(cfg DiskSinkConfig) (*DiskSink, error) {
	if cfg.Path == "" {
		return nil, errors.New("disk sink path is required")
	}
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = 100 * 1024 * 1024 // 100MB
	}
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = 10
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(cfg.Path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	s := &DiskSink{cfg: cfg, file: f}
	if st, err := f.Stat(); err == nil {
		s.pos = st.Size()
	}

	return s, nil
}

func (s *DiskSink) WriteEvent(ctx context.Context, data []byte, key []byte) error {
	return s.WriteBatch(ctx, [][]byte{data})
}

func (s *DiskSink) WriteBatch(ctx context.Context, events [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pos >= s.cfg.MaxFileSize {
		if err := s.rotateFile(); err != nil {
			return err
		}
	}

	var payload []byte
	var marshalErrs []error
	for _, e := range events {
		event := Event{
			Timestamp: time.Now(),
			Payload:   e,
		}
		if cfgKey := s.cfg.EncryptKey; cfgKey != "" {
			encrypted, err := encryptPayload(event.Payload, cfgKey)
			if err != nil {
				marshalErrs = append(marshalErrs, fmt.Errorf("encrypt event payload: %w", err))
				continue
			}
			event.Payload = encrypted
		}
		data, err := json.Marshal(event)
		if err != nil {
			marshalErrs = append(marshalErrs, fmt.Errorf("marshal event %s: %w", event.ID, err))
			continue
		}
		payload = append(payload, data...)
		payload = append(payload, '\n')
	}
	if len(marshalErrs) > 0 {
		return errors.Join(marshalErrs...)
	}

	n, err := s.file.Write(payload)
	if err != nil {
		return err
	}
	s.pos += int64(n)

	return nil
}

func (s *DiskSink) rotateFile() error {
	filename := s.cfg.Path
	dir := filepath.Dir(filename)
	base := filepath.Base(filename)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]

	// Backups are numbered .0 (newest) through .(MaxFiles-1) (oldest). Shift
	// each existing backup up by one slot before the active file is renamed to
	// .0, so older generations are retained up to MaxFiles. Iterating in
	// descending order moves a source before the next iteration can overwrite
	// it; the oldest backup is overwritten and thereby discarded.
	for i := s.cfg.MaxFiles - 2; i >= 0; i-- {
		oldPath := filepath.Join(dir, fmt.Sprintf("%s.%d%s", name, i, ext))
		newPath := filepath.Join(dir, fmt.Sprintf("%s.%d%s", name, i+1, ext))
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	currentPath := filepath.Join(dir, fmt.Sprintf("%s.0%s", name, ext))
	if err := s.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(s.cfg.Path, currentPath); err != nil {
		f, openErr := os.OpenFile(s.cfg.Path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
		if openErr != nil {
			return err
		}
		s.file = f
		s.pos = 0
		return err
	}

	f, err := os.OpenFile(s.cfg.Path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	s.file = f
	s.pos = 0

	return nil
}

func (s *DiskSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Sync()
}

func (s *DiskSink) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func encryptPayload(payload []byte, key string) ([]byte, error) {
	if key == "" {
		cp := make([]byte, len(payload))
		copy(cp, payload)
		return cp, nil
	}
	derived := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, payload, nil)
	buf := append(nonce, sealed...)
	encoded := base64.StdEncoding.EncodeToString(buf)
	return []byte("enc:" + encoded), nil
}
