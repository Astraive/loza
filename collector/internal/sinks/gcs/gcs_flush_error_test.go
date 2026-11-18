package gcs

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// failingTransport makes every GCS upload fail without any network access, so
// the ticker-driven flush exercises a real putObject error path.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("injected GCS failure")
}

// syncBuffer is a concurrency-safe writer because log and the background
// flush goroutine write while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newFailingClient(t *testing.T) *storage.Client {
	t.Helper()
	client, err := storage.NewClient(
		context.Background(),
		option.WithoutAuthentication(),
		option.WithHTTPClient(&http.Client{Transport: failingTransport{}}),
	)
	if err != nil {
		t.Fatalf("create failing GCS client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestBackgroundFlushErrorIsSurfaced reproduces the reported defect: the
// ticker path used to discard the error from flushBatch entirely. With an
// ErrorHandler configured, that failure must now be observable.
func TestBackgroundFlushErrorIsSurfaced(t *testing.T) {
	errCh := make(chan error, 1)
	gotSink, err := New(Config{
		Client:        newFailingClient(t),
		Bucket:        "bucket",
		BatchSize:     100,
		FlushInterval: 10 * time.Millisecond,
		ErrorHandler: func(flushErr error) {
			select {
			case errCh <- flushErr:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := gotSink.(*sink)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	// Buffer a record so the background flush has a batch to upload.
	if _, _, err := s.appendLocked([]byte(`{"id":"e1"}`)); err != nil {
		t.Fatalf("buffer event: %v", err)
	}

	select {
	case flushErr := <-errCh:
		if flushErr == nil {
			t.Fatal("handler received a nil error")
		}
		if !strings.Contains(flushErr.Error(), "gcs:") {
			t.Fatalf("expected a wrapped GCS error, got %v", flushErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background flush error was not surfaced to the handler")
	}
}

// TestBackgroundFlushErrorIsLoggedWhenNoHandler verifies the default fallback
// so failures are still observable when no ErrorHandler is configured.
func TestBackgroundFlushErrorIsLoggedWhenNoHandler(t *testing.T) {
	buf := &syncBuffer{}
	previous := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	gotSink, err := New(Config{
		Client:        newFailingClient(t),
		Bucket:        "bucket",
		BatchSize:     100,
		FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := gotSink.(*sink)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	if _, _, err := s.appendLocked([]byte(`{"id":"e1"}`)); err != nil {
		t.Fatalf("buffer event: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "background flush error") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background flush error was not logged, got %q", buf.String())
}

func TestReportFlushErrorIgnoresNil(t *testing.T) {
	called := false
	s := &sink{cfg: Config{ErrorHandler: func(error) { called = true }}}
	s.reportFlushError(nil)
	if called {
		t.Fatal("handler called for a nil error")
	}
}
