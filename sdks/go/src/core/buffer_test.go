package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEventBuffer_BatchSizeFlush tests that buffer flushes when batch_size is reached.
// Requirement 33.4
func TestEventBuffer_BatchSizeFlush(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     10,
		FlushInterval: time.Hour, // Long interval to avoid time-based flushes
		MaxBufferSize: 100,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 9 events - should not flush
	for i := 0; i < 9; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Give a moment for any potential flush
	time.Sleep(50 * time.Millisecond)

	if flushCount.Load() != 0 {
		t.Errorf("Expected no flush with 9 events, got %d flushes", flushCount.Load())
	}

	// Add 10th event - should trigger flush
	ev := &Event{EventID: NewUUIDv7()}
	err := buf.Add(ctx, ev, []byte("test"))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Give a moment for flush to complete
	time.Sleep(50 * time.Millisecond)

	if flushCount.Load() != 1 {
		t.Errorf("Expected 1 flush after 10 events, got %d", flushCount.Load())
	}

	if flushedEvents.Load() != 10 {
		t.Errorf("Expected 10 flushed events, got %d", flushedEvents.Load())
	}
}

// TestEventBuffer_IntervalFlush tests that buffer flushes when flush_interval elapses.
// Requirement 33.5
func TestEventBuffer_IntervalFlush(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,                    // Large batch size to avoid batch-based flush
		FlushInterval: 100 * time.Millisecond, // Short interval for testing
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 5 events
	for i := 0; i < 5; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Wait for interval to elapse
	time.Sleep(150 * time.Millisecond)

	if flushCount.Load() < 1 {
		t.Errorf("Expected at least 1 flush after interval, got %d", flushCount.Load())
	}

	if flushedEvents.Load() != 5 {
		t.Errorf("Expected 5 flushed events, got %d", flushedEvents.Load())
	}
}

// TestEventBuffer_ManualFlush tests that Flush() immediately flushes buffered events.
// Requirement 33.6
func TestEventBuffer_ManualFlush(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,
		FlushInterval: time.Hour,
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 3 events
	for i := 0; i < 3; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Manually flush
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if flushCount.Load() != 1 {
		t.Errorf("Expected 1 flush, got %d", flushCount.Load())
	}

	if flushedEvents.Load() != 3 {
		t.Errorf("Expected 3 flushed events, got %d", flushedEvents.Load())
	}

	// Buffer should be empty now
	if buf.Size() != 0 {
		t.Errorf("Expected empty buffer after flush, got size %d", buf.Size())
	}
}

// TestEventBuffer_CloseFlush tests that Close() flushes remaining events.
// Requirement 33.7
func TestEventBuffer_CloseFlush(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,
		FlushInterval: time.Hour,
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})

	ctx := context.Background()

	// Add 7 events
	for i := 0; i < 7; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Close should flush
	err := buf.Close(ctx)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if flushCount.Load() != 1 {
		t.Errorf("Expected 1 flush on close, got %d", flushCount.Load())
	}

	if flushedEvents.Load() != 7 {
		t.Errorf("Expected 7 flushed events, got %d", flushedEvents.Load())
	}
}

// TestEventBuffer_AddAfterClose verifies that events added after Close are
// rejected rather than buffered with no flush loop left to drain them.
func TestEventBuffer_AddAfterClose(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,
		FlushInterval: time.Hour, // Long interval to avoid time-based flushes
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})

	ctx := context.Background()

	// Add events before close; they should be flushed by Close.
	for i := 0; i < 3; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		if err := buf.Add(ctx, ev, []byte("test")); err != nil {
			t.Fatalf("Add before close failed: %v", err)
		}
	}

	if err := buf.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	flushCountAfterClose := flushCount.Load()
	flushedEventsAfterClose := flushedEvents.Load()
	if flushCountAfterClose != 1 || flushedEventsAfterClose != 3 {
		t.Fatalf("Expected Close to flush 3 events once, got %d flushes / %d events",
			flushCountAfterClose, flushedEventsAfterClose)
	}

	// Adding after Close must be rejected, not silently buffered.
	ev := &Event{EventID: NewUUIDv7()}
	if err := buf.Add(ctx, ev, []byte("test")); !errors.Is(err, ErrBufferClosed) {
		t.Fatalf("Expected ErrBufferClosed after Close, got %v", err)
	}

	// The rejected event must not be stranded in the buffer.
	if buf.Size() != 0 {
		t.Errorf("Expected empty buffer after rejected Add, got size %d", buf.Size())
	}

	// A second Close must not flush any stranded events.
	if err := buf.Close(ctx); err != nil {
		t.Fatalf("Second Close failed: %v", err)
	}
	if got := flushCount.Load(); got != flushCountAfterClose {
		t.Errorf("Expected no additional flushes after second Close, got %d -> %d", flushCountAfterClose, got)
	}
	if got := flushedEvents.Load(); got != flushedEventsAfterClose {
		t.Errorf("Expected no additional flushed events after second Close, got %d -> %d", flushedEventsAfterClose, got)
	}
}

// TestEventBuffer_OverflowDropsOldest tests that buffer drops oldest events when max_buffer_size is exceeded.
// Requirement 33.9
func TestEventBuffer_OverflowDropsOldest(t *testing.T) {
	var mu sync.Mutex
	var flushedEventIDs []string

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,
		FlushInterval: time.Hour,
		MaxBufferSize: 10, // Small buffer to test overflow
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			mu.Lock()
			defer mu.Unlock()
			for _, ev := range events {
				flushedEventIDs = append(flushedEventIDs, ev.EventID)
			}
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 15 events (5 more than max)
	eventIDs := make([]string, 15)
	for i := 0; i < 15; i++ {
		id := NewUUIDv7()
		eventIDs[i] = id
		ev := &Event{EventID: id}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Check dropped count
	if buf.DroppedCount() != 5 {
		t.Errorf("Expected 5 dropped events, got %d", buf.DroppedCount())
	}

	// Buffer should contain only the last 10 events
	if buf.Size() != 10 {
		t.Errorf("Expected buffer size 10, got %d", buf.Size())
	}

	// Flush and verify we got the last 10 events (not the first 5)
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(flushedEventIDs) != 10 {
		t.Fatalf("Expected 10 flushed events, got %d", len(flushedEventIDs))
	}

	// Verify the flushed events are the last 10 (indices 5-14)
	for i, id := range flushedEventIDs {
		expectedID := eventIDs[i+5]
		if id != expectedID {
			t.Errorf("Event %d: expected ID %s, got %s", i, expectedID, id)
		}
	}
}

// TestEventBuffer_EventOrderPreserved tests that event order is preserved within batches.
// Requirement 33.11
func TestEventBuffer_EventOrderPreserved(t *testing.T) {
	var mu sync.Mutex
	var flushedEventIDs []string

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     5,
		FlushInterval: time.Hour,
		MaxBufferSize: 100,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			mu.Lock()
			defer mu.Unlock()
			for _, ev := range events {
				flushedEventIDs = append(flushedEventIDs, ev.EventID)
			}
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 12 events (will trigger 2 flushes of 5, leaving 2 buffered)
	eventIDs := make([]string, 12)
	for i := 0; i < 12; i++ {
		id := NewUUIDv7()
		eventIDs[i] = id
		ev := &Event{EventID: id}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		// Small delay to ensure IDs are generated in order
		time.Sleep(time.Millisecond)
	}

	// Flush remaining events
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify all 12 events were flushed in order
	if len(flushedEventIDs) != 12 {
		t.Fatalf("Expected 12 flushed events, got %d", len(flushedEventIDs))
	}

	for i, id := range flushedEventIDs {
		if id != eventIDs[i] {
			t.Errorf("Event %d: expected ID %s, got %s (order not preserved)", i, eventIDs[i], id)
		}
	}
}

// TestEventBuffer_ConcurrentAdds tests that buffer handles concurrent adds correctly.
func TestEventBuffer_ConcurrentAdds(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     50,
		FlushInterval: 100 * time.Millisecond,
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			flushedEvents.Add(int32(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()
	const numGoroutines = 10
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Add events concurrently
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				ev := &Event{EventID: NewUUIDv7()}
				_ = buf.Add(ctx, ev, []byte("test"))
			}
		}()
	}

	wg.Wait()

	// Flush remaining events
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Wait a bit for any pending flushes
	time.Sleep(50 * time.Millisecond)

	totalEvents := numGoroutines * eventsPerGoroutine
	if int(flushedEvents.Load()) != totalEvents {
		t.Errorf("Expected %d total flushed events, got %d", totalEvents, flushedEvents.Load())
	}
}

// TestEventBuffer_EmptyFlush tests that flushing an empty buffer is a no-op.
func TestEventBuffer_EmptyFlush(t *testing.T) {
	var flushCount atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     10,
		FlushInterval: time.Hour,
		MaxBufferSize: 100,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Flush empty buffer
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if flushCount.Load() != 0 {
		t.Errorf("Expected no flush for empty buffer, got %d flushes", flushCount.Load())
	}
}

// TestEventBuffer_Size tests the Size() method.
// Requirement 33.10
func TestEventBuffer_Size(t *testing.T) {
	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     100,
		FlushInterval: time.Hour,
		MaxBufferSize: 1000,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	if buf.Size() != 0 {
		t.Errorf("Expected initial size 0, got %d", buf.Size())
	}

	// Add 5 events
	for i := 0; i < 5; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	if buf.Size() != 5 {
		t.Errorf("Expected size 5, got %d", buf.Size())
	}

	// Flush
	err := buf.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if buf.Size() != 0 {
		t.Errorf("Expected size 0 after flush, got %d", buf.Size())
	}
}

// TestEventBuffer_MultipleFlushes tests multiple flush cycles.
func TestEventBuffer_MultipleFlushes(t *testing.T) {
	var flushCount atomic.Int32
	var totalFlushed atomic.Int32

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     5,
		FlushInterval: time.Hour,
		MaxBufferSize: 100,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			flushCount.Add(1)
			totalFlushed.Add(int32(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	// Add 15 events (should trigger 3 flushes)
	for i := 0; i < 15; i++ {
		ev := &Event{EventID: NewUUIDv7()}
		err := buf.Add(ctx, ev, []byte("test"))
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Give time for flushes to complete
	time.Sleep(50 * time.Millisecond)

	if flushCount.Load() != 3 {
		t.Errorf("Expected 3 flushes, got %d", flushCount.Load())
	}

	if totalFlushed.Load() != 15 {
		t.Errorf("Expected 15 total flushed events, got %d", totalFlushed.Load())
	}
}

// TestEventBuffer_DefaultConfig tests that default configuration values are applied.
func TestEventBuffer_DefaultConfig(t *testing.T) {
	buf := NewEventBuffer(EventBufferConfig{
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			return nil
		},
	})
	defer buf.Close(context.Background())

	if buf.batchSize != 100 {
		t.Errorf("Expected default batch size 100, got %d", buf.batchSize)
	}

	if buf.flushInterval != 5*time.Second {
		t.Errorf("Expected default flush interval 5s, got %s", buf.flushInterval)
	}

	if buf.maxBufferSize != 10000 {
		t.Errorf("Expected default max buffer size 10000, got %d", buf.maxBufferSize)
	}
}

// TestEventBuffer_ConcurrentAddDoesNotDropPendingBatch is a regression test for
// the race where Add() released the mutex before flushing a full batch. A
// concurrent Add() could then grab the lock, see a full buffer and drop the
// oldest event from a batch that was still pending a flush. With
// batchSize == maxBufferSize this window is acute: every event that fills the
// buffer is immediately ripe for an overflow drop. The fix snapshots the batch
// while holding the same lock that guards the overflow drop, so no event from
// a full batch can be evicted before it is handed to the flush function.
func TestEventBuffer_ConcurrentAddDoesNotDropPendingBatch(t *testing.T) {
	const (
		batchSize     = 1 // maxBufferSize == batchSize makes the race window acute
		numGoroutines = 16
		eventsPerGor  = 500
	)

	var (
		flushedTotal atomic.Int64
		mu           sync.Mutex
		seen         = make(map[string]int)
	)

	buf := NewEventBuffer(EventBufferConfig{
		BatchSize:     batchSize,
		FlushInterval: time.Hour, // avoid time-based flushes
		MaxBufferSize: batchSize,
		FlushFunc: func(ctx context.Context, events []*Event, encoded [][]byte) error {
			mu.Lock()
			for _, ev := range events {
				seen[ev.EventID]++
			}
			mu.Unlock()
			flushedTotal.Add(int64(len(events)))
			return nil
		},
	})
	defer buf.Close(context.Background())

	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < eventsPerGor; j++ {
				id := fmt.Sprintf("g%d-%d", g, j)
				_ = buf.Add(ctx, &Event{EventID: id}, []byte(id))
			}
		}(g)
	}
	wg.Wait()

	// Flush any remainder so every accepted event is accounted for.
	_ = buf.Flush(ctx)

	total := numGoroutines * eventsPerGor
	if got := buf.DroppedCount(); got != 0 {
		t.Errorf("expected 0 dropped events when batchSize == maxBufferSize, got %d", got)
	}
	if got := flushedTotal.Load(); got != int64(total) {
		t.Errorf("expected %d flushed events, got %d", total, got)
	}

	mu.Lock()
	defer mu.Unlock()
	for g := 0; g < numGoroutines; g++ {
		for j := 0; j < eventsPerGor; j++ {
			id := fmt.Sprintf("g%d-%d", g, j)
			if seen[id] != 1 {
				t.Fatalf("event %s flushed %d times, want exactly 1", id, seen[id])
			}
		}
	}
}
