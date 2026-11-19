package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

func TestNewRejectsInvalidURL(t *testing.T) {
	_, err := New(Config{URL: "://bad"})
	if err == nil {
		t.Fatalf("expected invalid URL error")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlushPushesLokiPayload(t *testing.T) {
	var receivedBody []byte
	var receivedTenant string
	var reqCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		receivedTenant = r.Header.Get("X-Scope-OrgID")
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		receivedBody = append([]byte(nil), body...)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rawSink, err := New(Config{
		URL:           srv.URL + "/loki/api/core/push",
		TenantID:      "tenant-a",
		Labels:        map[string]string{"service": "checkout"},
		BatchSize:     10,
		FlushInterval: time.Hour,
		Client:        srv.Client(),
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := rawSink.(*sink)
	defer func() { _ = s.Close(context.Background()) }()

	ev := &collectorevent.Event{Timestamp: time.Unix(1, 42)}
	if err := s.WriteEvent(context.Background(), []byte(`{"event":"checkout.request"}`), ev); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if reqCount.Load() != 0 {
		t.Fatalf("expected no push before flush, got %d", reqCount.Load())
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if reqCount.Load() != 1 {
		t.Fatalf("expected 1 push after flush, got %d", reqCount.Load())
	}
	if receivedTenant != "tenant-a" {
		t.Fatalf("unexpected tenant header: %q", receivedTenant)
	}

	var payload lokiPushPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Streams) != 1 {
		t.Fatalf("expected one stream, got %d", len(payload.Streams))
	}
	if payload.Streams[0].Stream["service"] != "checkout" {
		t.Fatalf("unexpected stream labels: %+v", payload.Streams[0].Stream)
	}
	if len(payload.Streams[0].Values) != 1 {
		t.Fatalf("expected one value, got %d", len(payload.Streams[0].Values))
	}
	if got := payload.Streams[0].Values[0][1]; got != `{"event":"checkout.request"}` {
		t.Fatalf("unexpected pushed line: %q", got)
	}
	if got := payload.Streams[0].Values[0][0]; got != "1000000042" {
		t.Fatalf("unexpected timestamp value: %q", got)
	}
}

func TestFlushRestoreOnFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rawSink, err := New(Config{
		URL:           srv.URL,
		BatchSize:     10,
		FlushInterval: time.Hour,
		Client:        srv.Client(),
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := rawSink.(*sink)
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"retry-me"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("expected flush to retry and succeed, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 push attempts, got %d", calls.Load())
	}
}

// TestCloseFinalFlushFailureDropsExplicitly reproduces the reported defect:
// a failed final push during Close used to be restored into s.batch after the
// flush loop was stopped, leaving entries stranded in a sink that can never
// flush them again. The fix reports the drop explicitly and keeps the sink
// free of unflushable entries.
func TestCloseFinalFlushFailureDropsExplicitly(t *testing.T) {
func TestCloseReturnsFlushErrorToAllCallers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer srv.Close()

	rawSink, err := New(Config{
		URL:           srv.URL,
		BatchSize:     10,
		FlushInterval: time.Hour,
		Client:        srv.Client(),
func TestPeriodicFlushErrorIsSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("loki unavailable"))
	}))
	defer srv.Close()

	errCh := make(chan error, 8)
	rawSink, err := New(Config{
		URL:           srv.URL,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Timeout:       200 * time.Millisecond,
		Client:        srv.Client(),
		OnFlushError: func(err error) {
			errCh <- err
		},
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := rawSink.(*sink)

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"stranded"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}

	err = s.Close(context.Background())
	if err == nil {
		t.Fatal("expected Close to report the failed final flush")
	}
	if !strings.Contains(err.Error(), "dropping 1 buffered entries") {
		t.Fatalf("Close must explicitly report the dropped entries, got: %v", err)
	}

	s.mu.Lock()
	remaining := len(s.batch)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("closed sink must not retain unflushable entries, got %d", remaining)
	}
}

// TestCloseWaitsForInflightPush verifies that Close waits for a push already
// in flight before performing the final flush, so a failed in-flight push has
// been restored and its entries are picked up by the final flush.
func TestCloseWaitsForInflightPush(t *testing.T) {
	pushStarted := make(chan struct{})
	releasePush := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(pushStarted) })
		<-releasePush
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rawSink, err := New(Config{
		URL: srv.URL,
		// BatchSize 1 makes appendEntry hand the batch straight to WriteEvent,
		// which then blocks in an in-flight push.
		BatchSize:     1,
		FlushInterval: time.Hour,
		Client:        srv.Client(),
// closeInflightTransport blocks the first push (the in-flight WriteEvent)
// until released, fails the first three pushes (WriteEvent's three attempts)
// and makes later pushes succeed. It records whether the entry was ever
// successfully pushed.
type closeInflightTransport struct {
	mu        sync.Mutex
	calls     int
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	wantLine  string

	okWithEntry bool
}

func (tr *closeInflightTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()

	tr.mu.Lock()
	tr.calls++
	n := tr.calls
	tr.mu.Unlock()

	if n == 1 {
		tr.startOnce.Do(func() { close(tr.started) })
		<-tr.release
	}

	status := http.StatusServiceUnavailable
	if n > 3 {
		status = http.StatusNoContent
		if strings.Contains(string(body), tr.wantLine) {
			tr.mu.Lock()
			tr.okWithEntry = true
			tr.mu.Unlock()
		}
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestCloseWaitsForInflightWriteEvent(t *testing.T) {
	const line = `{"event":"inflight"}`
	tr := &closeInflightTransport{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		wantLine: "inflight", // JSON-escaped quotes make the full line unreliable to match
	}

	rawSink, err := New(Config{
		URL:           "http://loki.example/loki/api/v1/push",
		BatchSize:     1, // appendEntry hands off a batch on the first event
		FlushInterval: time.Hour,
		Client:        &http.Client{Transport: tr},
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := rawSink.(*sink)

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWrite()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- s.WriteEvent(writeCtx, []byte(`{"event":"inflight"}`), nil)
	}()

	select {
	case <-pushStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("push did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the in-flight push completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releasePush)

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write event: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write did not complete")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not complete")
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"periodic"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}

	select {
	case got := <-errCh:
		if got == nil {
			t.Fatalf("expected a non-nil flush error")
		}
		if !strings.Contains(got.Error(), "push batch failed") {
			t.Fatalf("unexpected flush error: %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("periodic flush error was not surfaced through OnFlushError")
	}

	// Stop the background loop before inspecting the retained batch so the
	// assertion does not race with a concurrent flush. Close is expected to
	// fail here because the test server never accepts a push.
	_ = s.Close(context.Background())
	s.mu.Lock()
	retained := len(s.batch)
	s.mu.Unlock()
	if retained != 1 {
		t.Fatalf("expected the failed batch to be retained in memory, got %d entries", retained)
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- s.WriteEvent(context.Background(), []byte(line), nil)
	}()

	select {
	case <-tr.started:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight WriteEvent push never started")
	}

	// Close while the WriteEvent is still pushing. With the fix, Close blocks
	// in inflight.Wait(); without it, Close does an empty final Flush and
	// returns right away. Wait for whichever happens before releasing the push.
	closeErr := make(chan error, 1)
	closeDone := make(chan struct{})
	go func() {
		closeErr <- s.Close(context.Background())
		close(closeDone)
	}()

	select {
	case <-closeDone:
		// Buggy path: Close returned before the in-flight push/restore.
	case <-time.After(300 * time.Millisecond):
		// Fixed path: Close is waiting for the in-flight WriteEvent.
	}

	close(tr.release)

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("expected the in-flight WriteEvent push to fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteEvent did not return")
	}
	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.okWithEntry {
		t.Fatalf("restored batch was never pushed by Close's final Flush (calls=%d)", tr.calls)
	// Leave a batch pending so the flush performed by Close fails upstream.
	if err := s.WriteEvent(context.Background(), []byte(`{"event":"close.flush"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}

	const callers = 8
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = s.Close(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Errorf("Close caller %d got nil; want flush error propagated to every caller", i)
			continue
		}
		if !strings.Contains(err.Error(), "502") {
			t.Errorf("Close caller %d got unexpected error: %v", i, err)
		}
	}
}
