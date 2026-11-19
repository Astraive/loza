package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	lozav1 "github.com/astraive/loza/gen/go/loza/core"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeCortexBridgeServer struct {
	lozav1.UnimplementedCortexServiceServer
	mu        sync.Mutex
	batches   []*lozav1.IngestBatchRequest
	callCount int
	failFirst int // number of initial IngestBatch calls that fail transiently
}

func (f *fakeCortexBridgeServer) IngestBatch(_ context.Context, req *lozav1.IngestBatchRequest) (*lozav1.IngestBatchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.callCount <= f.failFirst {
		return nil, status.Error(codes.Unavailable, "transient cortex outage")
	}
	f.batches = append(f.batches, req)
	return &lozav1.IngestBatchResponse{Status: "accepted", Count: int32(len(req.Events))}, nil
}

func (f *fakeCortexBridgeServer) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

func (f *fakeCortexBridgeServer) lastBatch() *lozav1.IngestBatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		return nil
	}
	return f.batches[len(f.batches)-1]
}

func (f *fakeCortexBridgeServer) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func startFakeCortexBridge(t *testing.T) (string, *fakeCortexBridgeServer, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	fake := &fakeCortexBridgeServer{}
	lozav1.RegisterCortexServiceServer(srv, fake)
	go func() {
		_ = srv.Serve(lis)
	}()
	return lis.Addr().String(), fake, func() {
		srv.GracefulStop()
		_ = lis.Close()
	}
}

func TestCortexBridgeSyncDelivery(t *testing.T) {
	endpoint, fake, stop := startFakeCortexBridge(t)
	defer stop()

	bridge, err := newCortexBridgeClient(collectorConfig{
		cortexBridgeEnabled:  true,
		cortexBridgeMode:     "sync",
		cortexBridgeEndpoint: endpoint,
		cortexBridgeInsecure: true,
		cortexBridgeTimeout:  2 * time.Second,
	}, nil)
	require.NoError(t, err)
	defer bridge.Close()

	raw := []byte(`{"event_id":"evt-sync","timestamp":"2026-05-17T12:00:00Z","service":"checkout","kind":"http","trace_id":"tr_1"}`)
	require.NoError(t, bridge.Deliver(context.Background(), [][]byte{raw}))

	require.Eventually(t, func() bool { return fake.batchCount() == 1 }, 3*time.Second, 20*time.Millisecond)
	batch := fake.lastBatch()
	require.NotNil(t, batch)
	require.Len(t, batch.Events, 1)
	require.Equal(t, "evt-sync", batch.Events[0].EventId)
	require.Equal(t, "checkout", batch.Events[0].Service)
	require.Equal(t, lozav1.EventKind_EVENT_KIND_EVENT, batch.Events[0].Kind)
	require.Equal(t, "tr_1", batch.Events[0].TraceId)
	require.NotNil(t, batch.Events[0].Attrs)
}

func TestCortexBridgeAsyncFlush(t *testing.T) {
	endpoint, fake, stop := startFakeCortexBridge(t)
	defer stop()

	bridge, err := newCortexBridgeClient(collectorConfig{
		cortexBridgeEnabled:    true,
		cortexBridgeMode:       "async",
		cortexBridgeEndpoint:   endpoint,
		cortexBridgeInsecure:   true,
		cortexBridgeTimeout:    2 * time.Second,
		cortexBridgeBatchSize:  2,
		cortexBridgeFlushIntvl: 25 * time.Millisecond,
		cortexBridgeQueueSize:  8,
	}, nil)
	require.NoError(t, err)
	defer bridge.Close()

	rawA := []byte(`{"event_id":"evt-a","timestamp":"2026-05-17T12:00:00Z","service":"billing","kind":"event"}`)
	rawB := []byte(`{"event_id":"evt-b","timestamp":"2026-05-17T12:00:01Z","service":"billing","kind":"event"}`)
	require.NoError(t, bridge.Deliver(context.Background(), [][]byte{rawA, rawB}))

	require.Eventually(t, func() bool { return fake.batchCount() >= 1 }, 3*time.Second, 20*time.Millisecond)
	batch := fake.lastBatch()
	require.NotNil(t, batch)
	require.Len(t, batch.Events, 2)
}

// TestCortexBridgeDeliverAfterCloseDoesNotPanic is a deterministic regression
// for the send-on-closed-channel crash: Deliver used to send to c.queue after
// Close had closed it. It must now be rejected cleanly.
func TestCortexBridgeDeliverAfterCloseDoesNotPanic(t *testing.T) {
	bridge := &cortexBridgeClient{
		cfg:   collectorConfig{cortexBridgeMode: "async"},
		queue: make(chan []byte, 4),
	}
	require.NoError(t, bridge.Close())

	raw := []byte(`{"event_id":"evt-after-close","timestamp":"2026-05-17T12:00:00Z","service":"checkout"}`)
	require.ErrorIs(t, bridge.Deliver(context.Background(), [][]byte{raw}), errCortexBridgeClosed)
}

// TestCortexBridgeDeliverConcurrentWithClose exercises the race described in
// the finding: many Deliver calls in flight while Close closes the queue. It
// must complete without a "send on closed channel" panic (also under -race).
func TestCortexBridgeDeliverConcurrentWithClose(t *testing.T) {
	bridge := &cortexBridgeClient{
		cfg:   collectorConfig{cortexBridgeMode: "async"},
		queue: make(chan []byte, 4096),
	}
	raw := []byte(`{"event_id":"evt-race","timestamp":"2026-05-17T12:00:00Z","service":"checkout"}`)

	const workers = 8
	const iterations = 500
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iterations; j++ {
				_ = bridge.Deliver(context.Background(), [][]byte{raw})
			}
		}()
	}
	close(start)
	require.NoError(t, bridge.Close())
	wg.Wait()

	require.ErrorIs(t, bridge.Deliver(context.Background(), [][]byte{raw}), errCortexBridgeClosed)
}

// TestCortexBridgeAsyncRetryOnTransientFailure reproduces the reported defect:
// before the fix, a single failed flush silently discarded the batch. The
// event must instead be retried and delivered once Cortex recovers.
func TestCortexBridgeAsyncRetryOnTransientFailure(t *testing.T) {
	endpoint, fake, stop := startFakeCortexBridge(t)
	defer stop()
	fake.mu.Lock()
	fake.failFirst = 1 // first flush attempt fails; the retry should succeed
	fake.mu.Unlock()

	metrics := &collectorMetrics{}
	bridge, err := newCortexBridgeClient(collectorConfig{
		cortexBridgeEnabled:    true,
		cortexBridgeMode:       "async",
		cortexBridgeEndpoint:   endpoint,
		cortexBridgeInsecure:   true,
		cortexBridgeTimeout:    2 * time.Second,
		cortexBridgeBatchSize:  1,
		cortexBridgeFlushIntvl: 10 * time.Millisecond,
		cortexBridgeQueueSize:  8,
	}, metrics)
	require.NoError(t, err)
	defer bridge.Close()

	raw := []byte(`{"event_id":"evt-retry","timestamp":"2026-05-17T12:00:00Z","service":"billing","kind":"event"}`)
	require.NoError(t, bridge.Deliver(context.Background(), [][]byte{raw}))

	require.Eventually(t, func() bool { return fake.batchCount() == 1 }, 3*time.Second, 20*time.Millisecond)
	batch := fake.lastBatch()
	require.NotNil(t, batch)
	require.Len(t, batch.Events, 1)
	require.Equal(t, "evt-retry", batch.Events[0].EventId)
	require.GreaterOrEqual(t, fake.totalCalls(), 2)
	require.EqualValues(t, 1, metrics.cortexBridgeErrors.Load())
	require.EqualValues(t, 1, metrics.cortexBridgeFlushes.Load())
	require.EqualValues(t, 1, metrics.cortexBridgeEvents.Load())
	require.EqualValues(t, 0, metrics.cortexBridgeDropped.Load())
}

// TestCortexBridgeAsyncDropsAfterRetryExhaustion verifies that permanent
// failures are bounded: the batch is retried a fixed number of times and only
// then dropped, with the loss recorded in the dropped-events metric.
func TestCortexBridgeAsyncDropsAfterRetryExhaustion(t *testing.T) {
	endpoint, fake, stop := startFakeCortexBridge(t)
	defer stop()
	fake.mu.Lock()
	fake.failFirst = cortexBridgeMaxFlushAttempts + 100 // never recover during the test
	fake.mu.Unlock()

	metrics := &collectorMetrics{}
	bridge, err := newCortexBridgeClient(collectorConfig{
		cortexBridgeEnabled:    true,
		cortexBridgeMode:       "async",
		cortexBridgeEndpoint:   endpoint,
		cortexBridgeInsecure:   true,
		cortexBridgeTimeout:    2 * time.Second,
		cortexBridgeBatchSize:  2,
		cortexBridgeFlushIntvl: 10 * time.Millisecond,
		cortexBridgeQueueSize:  8,
	}, metrics)
	require.NoError(t, err)
	defer bridge.Close()

	rawA := []byte(`{"event_id":"evt-drop-a","timestamp":"2026-05-17T12:00:00Z","service":"billing","kind":"event"}`)
	rawB := []byte(`{"event_id":"evt-drop-b","timestamp":"2026-05-17T12:00:01Z","service":"billing","kind":"event"}`)
	require.NoError(t, bridge.Deliver(context.Background(), [][]byte{rawA, rawB}))

	require.Eventually(t, func() bool { return metrics.cortexBridgeDropped.Load() == 2 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, cortexBridgeMaxFlushAttempts, fake.totalCalls())
	require.EqualValues(t, 0, fake.batchCount())
	require.EqualValues(t, cortexBridgeMaxFlushAttempts, metrics.cortexBridgeErrors.Load())
	require.EqualValues(t, 0, metrics.cortexBridgeFlushes.Load())
	require.EqualValues(t, 0, metrics.cortexBridgeEvents.Load())
}
