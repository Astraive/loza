package httpbatch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSinkBoundsBufferOnOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var drops atomic.Int64
	got, err := New(Config{
		URL:           server.URL,
		BatchSize:     1000,
		FlushInterval: time.Hour,
		MaxBufferSize: 3,
		OnDrop:        func(string) { drops.Add(1) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	impl := got.(*sink)
	for i := 0; i < 10; i++ {
		if err := got.WriteEvent(context.Background(), []byte(fmt.Sprintf("event-%d\n", i)), nil); err != nil {
			t.Fatalf("WriteEvent %d: %v", i, err)
		}
	}
	if n := impl.Size(); n != 3 {
		t.Fatalf("buffered events = %d, want MaxBufferSize 3", n)
	}
	if n := impl.DroppedCount(); n != 7 {
		t.Fatalf("dropped count = %d, want 7", n)
	}
	if n := drops.Load(); n != 7 {
		t.Fatalf("OnDrop notifications = %d, want 7", n)
	}
	_ = got.Close(context.Background())
}

func TestSinkBoundsRetainedFailedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "retry", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	got, err := New(Config{
		URL:           server.URL,
		BatchSize:     1,
		FlushInterval: time.Hour,
		MaxBufferSize: 2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	impl := got.(*sink)
	for i := 0; i < 6; i++ {
		_ = got.WriteEvent(context.Background(), []byte("event\n"), nil)
	}
	if n := impl.Size(); n > 2 {
		t.Fatalf("buffered events = %d, want <= MaxBufferSize 2", n)
	}
	if impl.DroppedCount() == 0 {
		t.Fatal("expected dropped-event metrics under sustained collector failure")
	}
	_ = got.Close(context.Background())
}

func TestSinkWriteEventUsesCallerContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	got, err := New(Config{URL: server.URL, BatchSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := got.WriteEvent(ctx, []byte("event\n"), nil); err == nil {
		t.Fatal("WriteEvent with canceled caller context unexpectedly succeeded")
	}
	_ = got.Close(context.Background())
}
