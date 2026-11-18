package s3

import (
	"context"
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
