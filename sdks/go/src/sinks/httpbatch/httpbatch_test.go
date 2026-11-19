package httpbatch

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// drainTrackingBody records whether the HTTP client consumed the response to
// EOF and closed it, which is what http.Transport requires to reuse a
// connection.
type drainTrackingBody struct {
	reader  io.Reader
	mu      sync.Mutex
	drained bool
	closed  bool
}

func (b *drainTrackingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		b.mu.Lock()
		b.drained = true
		b.mu.Unlock()
	}
	return n, err
}

func (b *drainTrackingBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

func TestNewRejectsMissingURL(t *testing.T) {
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("New empty URL error = %v", err)
	}
}

func TestBatchSinkAppliesDefaultsAndFlushesOnTicker(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	defaultSink, err := New(Config{URL: server.URL})
	if err != nil {
		t.Fatalf("New defaults: %v", err)
	}
	if err := defaultSink.Close(context.Background()); err != nil {
		t.Fatalf("Close defaults: %v", err)
	}

	sink, err := New(Config{URL: server.URL, FlushInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("New ticker: %v", err)
	}
	if err := sink.WriteEvent(context.Background(), []byte("tick\n"), nil); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("ticker did not flush buffered event")
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBatchSinkReturnsRequestErrors(t *testing.T) {
	malformed, err := New(Config{URL: "://invalid", BatchSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New malformed URL: %v", err)
	}
	if err := malformed.WriteEvent(context.Background(), []byte("invalid\n"), nil); err == nil {
		t.Fatal("WriteEvent with malformed URL returned nil")
	}
	_ = malformed.Close(context.Background())

	transportError := errors.New("transport unavailable")
	failed, err := New(Config{
		URL:           "http://collector.invalid",
		BatchSize:     1,
		FlushInterval: time.Hour,
		Client:        &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, transportError })},
	})
	if err != nil {
		t.Fatalf("New transport error sink: %v", err)
	}
	if err := failed.WriteEvent(context.Background(), []byte("fails\n"), nil); !errors.Is(err, transportError) {
		t.Fatalf("transport error = %v, want %v", err, transportError)
	}
	_ = failed.Close(context.Background())
}

func TestBatchSinkFlushesBatchesAndHonorsHeaders(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var encodings []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if r.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("X-Test = %q", r.Header.Get("X-Test"))
		}
		mu.Lock()
		bodies = append(bodies, body)
		encodings = append(encodings, r.Header.Get("Content-Encoding"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	got, err := New(Config{
		URL:           server.URL,
		Method:        http.MethodPut,
		Headers:       map[string]string{"X-Test": "yes"},
		BatchSize:     2,
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if got.Name() != "httpbatch" {
		t.Fatalf("Name = %q", got.Name())
	}
	if err := got.WriteEvent(ctx, []byte("one\n"), nil); err != nil {
		t.Fatalf("first WriteEvent: %v", err)
	}
	if err := got.WriteEvent(ctx, []byte("two\n"), nil); err != nil {
		t.Fatalf("second WriteEvent: %v", err)
	}
	impl := got.(*sink)
	if err := impl.WriteBatch(ctx, nil, nil); err != nil {
		t.Fatalf("empty WriteBatch: %v", err)
	}
	if err := impl.WriteBatch(ctx, [][]byte{[]byte("three\n")}, nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := got.Flush(ctx); err != nil {
		t.Fatalf("empty Flush: %v", err)
	}
	if err := got.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if string(bodies[0]) != "one\ntwo\n" || string(bodies[1]) != "three\n" {
		t.Fatalf("request bodies = %q and %q", bodies[0], bodies[1])
	}
	if encodings[0] != "" || encodings[1] != "" {
		t.Fatalf("unexpected content encodings: %v", encodings)
	}
}

func TestBatchSinkGzipAndStatusErrors(t *testing.T) {
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		received, _ = io.ReadAll(reader)
		_ = reader.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := New(Config{URL: server.URL, Gzip: true, BatchSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New gzip: %v", err)
	}
	if err := sink.WriteEvent(context.Background(), []byte("compressed\n"), nil); err != nil {
		t.Fatalf("gzip WriteEvent: %v", err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	if string(received) != "compressed\n" {
		t.Fatalf("decompressed payload = %q", received)
	}

	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failure", http.StatusBadGateway)
	}))
	defer failure.Close()
	bad, err := New(Config{URL: failure.URL, BatchSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New failure sink: %v", err)
	}
	if err := bad.WriteEvent(context.Background(), []byte("fails\n"), nil); err == nil || !strings.Contains(err.Error(), "unexpected status 502") {
		t.Fatalf("status error = %v", err)
	}
	if err := bad.Close(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected status 502") {
		t.Fatalf("Close after failed WriteEvent = %v, want retained-batch error", err)
	}
}

func TestBatchSinkRetainsFailedBatchAheadOfNewEvents(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		shouldFail := fail
		mu.Unlock()
		if shouldFail {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	got, err := New(Config{URL: server.URL, BatchSize: 1, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.WriteEvent(context.Background(), []byte("first\n"), nil); err == nil {
		t.Fatal("first delivery unexpectedly succeeded")
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	if err := got.WriteEvent(context.Background(), []byte("second\n"), nil); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	if err := got.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("request bodies = %q, want two attempts", bodies)
	}
	if bodies[0] != "first\n" || bodies[1] != "first\nsecond\n" {
		t.Fatalf("request bodies = %q, failed batch was not retried first", bodies)
	}
}

func TestBatchSinkWriteBatchPreservesOrderAfterFailedFlush(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		shouldFail := fail
		mu.Unlock()
		if shouldFail {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Large BatchSize and FlushInterval keep the timer/batch-size paths out of
	// the way so the only flush is the one we drive explicitly below.
	got, err := New(Config{URL: server.URL, BatchSize: 100, FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := got.WriteEvent(ctx, []byte("first\n"), nil); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	// Force a failed flush so "first\n" is retained at the head of the buffer.
	if err := got.Flush(ctx); err == nil {
		t.Fatal("first Flush unexpectedly succeeded")
	}

	mu.Lock()
	fail = false
	mu.Unlock()

	impl := got.(*sink)
	if err := impl.WriteBatch(ctx, [][]byte{[]byte("second\n")}, nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := got.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("request bodies = %q, want two attempts (retained batch must precede newer WriteBatch payload)", bodies)
	}
	if bodies[0] != "first\n" || bodies[1] != "first\nsecond\n" {
		t.Fatalf("request bodies = %q, WriteBatch payload overtook retained events", bodies)
	}
}

// TestBatchSinkCapsBufferOnSustainedFailures reproduces the unbounded growth
// reported in mass-scan finding MASS-0514: every failed send used to fold all
// concurrently buffered events back into the retained batch with no size cap.
func TestBatchSinkCapsBufferOnSustainedFailures(t *testing.T) {
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if fail {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dropped := make(chan error, 1)
	got, err := New(Config{
		URL:           server.URL,
		BatchSize:     2,
		FlushInterval: time.Hour,
		OnError: func(err error) {
			select {
			case dropped <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = got.Close(context.Background()) }()

	const events = 200
	for i := 0; i < events; i++ {
		// Every send fails, so the old implementation retained all events.
		_ = got.WriteEvent(context.Background(), []byte(fmt.Sprintf("event-%03d\n", i)), nil)
	}

	impl := got.(*sink)
	impl.mu.Lock()
	buffered := len(impl.buf)
	var last string
	if buffered > 0 {
		last = string(impl.buf[buffered-1])
	}
	cap := impl.maxBuffered()
	impl.mu.Unlock()

	if buffered > cap {
		t.Fatalf("buffered events = %d, want <= cap %d (unbounded growth on sustained failures)", buffered, cap)
	}
	if last != fmt.Sprintf("event-%03d\n", events-1) {
		t.Fatalf("newest retained event = %q, want %q (oldest should be dropped first)", last, fmt.Sprintf("event-%03d\n", events-1))
	}

	select {
	case err := <-dropped:
		if !strings.Contains(err.Error(), "dropped") {
			t.Fatalf("drop notification = %v, want a dropped-events error", err)
		}
	default:
		t.Fatal("no OnError notification for dropped events")
	}
}

func TestBatchSinkReportsTimerFailures(t *testing.T) {
	reported := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "retry", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	got, err := New(Config{
		URL:           server.URL,
		BatchSize:     100,
		FlushInterval: time.Millisecond,
		OnError: func(err error) {
			reported <- err
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.WriteEvent(context.Background(), []byte("timer\n"), nil); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "unexpected status 503") {
			t.Fatalf("timer error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timer failure was not reported")
	}
	if err := got.Close(context.Background()); err == nil {
		t.Fatal("Close should report the retained failed batch")
	}
}

func TestBatchSinkDrainsResponseBodyForConnectionReuse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "success", statusCode: http.StatusNoContent},
		{name: "error status", statusCode: http.StatusBadGateway, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &drainTrackingBody{reader: strings.NewReader("collector response payload")}
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.statusCode,
					Body:       body,
					Header:     make(http.Header),
				}, nil
			})}
			got, err := New(Config{
				URL:           "http://collector.invalid/ingest",
				BatchSize:     1,
				FlushInterval: time.Hour,
				Client:        client,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			sendErr := got.(*sink).WriteBatch(context.Background(), [][]byte{[]byte("event\n")}, nil)
			if tc.wantErr && sendErr == nil {
				t.Fatal("error status did not return an error")
			}
			if !tc.wantErr && sendErr != nil {
				t.Fatalf("WriteBatch: %v", sendErr)
			}
			if err := got.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}

			body.mu.Lock()
			defer body.mu.Unlock()
			if !body.drained {
				t.Fatal("response body was not read to EOF; connection cannot be reused")
			}
			if !body.closed {
				t.Fatal("response body was not closed")
			}
		})
	}
}
