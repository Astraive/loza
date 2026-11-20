package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// putRecorder blocks the first PUT so a test can hold a putObject call in
// flight while Close runs, then fails it to force restore().
type putRecorder struct {
	mu       sync.Mutex
	bodies   []string
	attempts int32
	entered  chan struct{}
	release  chan struct{}
}

func (p *putRecorder) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.bodies = append(p.bodies, string(body))
	attempt := atomic.AddInt32(&p.attempts, 1)
	p.mu.Unlock()
	if attempt == 1 {
		close(p.entered)
		<-p.release
		http.Error(w, "first put failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func newTestClient(t *testing.T, endpoint string) *awss3.Client {
	t.Helper()
	return awss3.New(awss3.Options{
		Region:           "us-east-1",
		BaseEndpoint:     aws.String(endpoint),
		UsePathStyle:     true,
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	})
}

// TestCloseWaitsForInFlightPut reproduces the case where a batched WriteEvent
// has an in-flight putObject when Close runs. The in-flight put fails after
// Close drains the buffer, so its restored record must be re-flushed by Close
// rather than silently lost while Close still reports success.
func TestCloseWaitsForInFlightPutAndFlushesRestoredRecord(t *testing.T) {
	rec := &putRecorder{entered: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(rec.serve))
	defer server.Close()

	got, err := New(Config{
		Client:        newTestClient(t, server.URL),
		Bucket:        "test-bucket",
		BatchSize:     1,
		FlushInterval: time.Hour,
		ObjectKeyFn:   func() string { return "events.ndjson" },
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := got.(*sink)

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- s.WriteEvent(context.Background(), []byte(`{"id":"in-flight"}`), nil)
	}()

	select {
	case <-rec.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight put did not reach the server")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()

	// Close must not report success while a putObject call is still in flight.
	select {
	case err := <-closeDone:
		close(rec.release)
		t.Fatalf("Close returned before the in-flight put finished: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	// Fail the in-flight put; WriteEvent restores its payload to the buffer.
	close(rec.release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the in-flight put finished")
	}

	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("expected the in-flight write to report the failed put")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write did not return")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := len(rec.bodies); got != 2 {
		t.Fatalf("expected restored record to be re-uploaded by Close, got %d PUTs: %q", got, rec.bodies)
	}
	if !strings.Contains(rec.bodies[1], "in-flight") {
		t.Fatalf("restored record was not re-uploaded: %q", rec.bodies)
	}
}

// TestCloseReportsFailedFinalFlush verifies the other half of the remedy: a
// restored record that still cannot be uploaded must surface as a Close error
// instead of being silently retained.
func TestCloseReportsFailedFinalFlush(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "put failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	got, err := New(Config{
		Client:        newTestClient(t, server.URL),
		Bucket:        "test-bucket",
		BatchSize:     1,
		FlushInterval: time.Hour,
		ObjectKeyFn:   func() string { return "events.ndjson" },
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := got.(*sink)

	if err := s.WriteEvent(context.Background(), []byte(`{"id":"pending"}`), nil); err == nil {
		t.Fatal("expected the failed write to be reported")
	}
	if err := s.Close(context.Background()); err == nil {
		t.Fatal("expected Close to report the failed final flush of the restored record")
	}
	if n := s.count; n == 0 {
		t.Fatal("expected the failed record to remain buffered for a future retry")
	}
}

// stallOnceHTTPClient stalls the first request until its context is cancelled
// (simulating a stalled S3 PutObject) and lets subsequent requests succeed.
type stallOnceHTTPClient struct {
	firstStarted chan struct{}
	startOnce    sync.Once
	mu           sync.Mutex
	calls        int
}

func (c *stallOnceHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	if call == 1 {
		c.startOnce.Do(func() { close(c.firstStarted) })
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"ETag": []string{`"stall-test-etag"`}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestCloseReturnsWhenPeriodicFlushStalls reproduces the reported defect: the
// ticker-driven flush used an unbounded context, so a stalled PutObject pinned
// the flush-loop goroutine and Close hung forever on wg.Wait(). With the fix,
// Close cancels the in-flight periodic flush and returns promptly.
func TestCloseReturnsWhenPeriodicFlushStalls(t *testing.T) {
	firstStarted := make(chan struct{})
	client := awss3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient:  &stallOnceHTTPClient{firstStarted: firstStarted},
	}, func(o *awss3.Options) {
		o.UsePathStyle = true
	})

	gotSink, err := New(Config{
		Client:        client,
		Bucket:        "bucket",
		BatchSize:     100, // avoid flushing on WriteEvent
		FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := gotSink.(*sink)

	if err := s.WriteEvent(context.Background(), []byte(`{"event_id":"e1"}`+"\n"), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Wait until the periodic flush is actually blocked inside PutObject.
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("periodic flush never started a PutObject")
	}

	done := make(chan error, 1)
	go func() { done <- s.Close(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung while a periodic flush was stalled")
	}
}

var errSimulatedPut = errors.New("simulated s3 put failure")

type failingHTTPClient struct{}

func (failingHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errSimulatedPut
}

// TestCloseReturnsFlushErrorOnEveryCall reproduces the case where the final
// flush fails: the error must be reported to the first and every later caller.
func TestCloseReturnsFlushErrorOnEveryCall(t *testing.T) {
	client := awss3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("id", "secret", ""),
		HTTPClient:  failingHTTPClient{},
		Retryer:     func() aws.Retryer { return aws.NopRetryer{} },
	})
	s := &sink{
		cfg: Config{
			Client:      client,
			Bucket:      "bucket",
			ObjectKeyFn: func() string { return "events.ndjson" },
		},
		batchSize: 10,
		stop:      make(chan struct{}),
	}

	if err := s.WriteEvent(context.Background(), []byte(`{"id":"e1"}`), nil); err != nil {
		t.Fatalf("write event failed: %v", err)
	}

	for i := 1; i <= 3; i++ {
		err := s.Close(context.Background())
		if !errors.Is(err, errSimulatedPut) {
			t.Fatalf("Close call %d returned %v; want final flush error", i, err)
		}
	}
}

func TestCloseReturnsNilWhenFlushSucceeds(t *testing.T) {
	s := &sink{
		batchSize: 10,
		stop:      make(chan struct{}),
	}
	for i := 1; i <= 2; i++ {
		if err := s.Close(context.Background()); err != nil {
			t.Fatalf("Close call %d returned %v; want nil", i, err)
		}
	}
}
