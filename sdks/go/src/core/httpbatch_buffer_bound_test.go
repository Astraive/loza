package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func failingCollector(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "retry", http.StatusServiceUnavailable)
	}))
}

func TestHTTPBatchSinkBoundsBufferOnOverflow(t *testing.T) {
	server := failingCollector(t)
	defer server.Close()

	var drops atomic.Int64
	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      server.URL,
		BatchSize:     1000, // avoid automatic flushes during this test
		FlushInterval: time.Hour,
		MaxBufferSize: 3,
		OnDrop:        func(string) { drops.Add(1) },
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink: %v", err)
	}
	impl := sink.(*httpBatchSink)
	for i := 0; i < 10; i++ {
		if err := sink.WriteEvent(context.Background(), []byte(fmt.Sprintf("event-%d\n", i)), nil); err != nil {
			t.Fatalf("WriteEvent %d: %v", i, err)
		}
	}
	if got := impl.Size(); got != 3 {
		t.Fatalf("buffered events = %d, want MaxBufferSize 3", got)
	}
	if got := impl.DroppedCount(); got != 7 {
		t.Fatalf("dropped count = %d, want 7", got)
	}
	if got := drops.Load(); got != 7 {
		t.Fatalf("OnDrop notifications = %d, want 7", got)
	}
	_ = sink.Close(context.Background())
}

func TestHTTPBatchSinkBoundsRetainedFailedBatch(t *testing.T) {
	server := failingCollector(t)
	defer server.Close()

	var drops atomic.Int64
	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      server.URL,
		BatchSize:     1, // every write triggers a failing flush
		FlushInterval: time.Hour,
		MaxBufferSize: 2,
		OnDrop:        func(string) { drops.Add(1) },
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink: %v", err)
	}
	impl := sink.(*httpBatchSink)
	for i := 0; i < 6; i++ {
		// Errors are expected: the collector keeps rejecting the batch.
		_ = sink.WriteEvent(context.Background(), []byte("event\n"), nil)
	}
	if got := impl.Size(); got > 2 {
		t.Fatalf("buffered events = %d, want <= MaxBufferSize 2", got)
	}
	if impl.DroppedCount() == 0 || drops.Load() == 0 {
		t.Fatal("expected dropped-event metrics under sustained collector failure")
	}
	_ = sink.Close(context.Background())
}

func TestHTTPBatchSinkWriteEventUsesCallerContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink, err := HTTPBatchSink(HTTPBatchSinkConfig{
		Endpoint:      server.URL,
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("HTTPBatchSink: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-canceled caller context must abort the write
	if err := sink.WriteEvent(ctx, []byte("event\n"), nil); err == nil {
		t.Fatal("WriteEvent with canceled caller context unexpectedly succeeded")
	}
	_ = sink.Close(context.Background())
}
