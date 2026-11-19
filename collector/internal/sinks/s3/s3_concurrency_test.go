package s3

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// scriptedHTTPClient intercepts PutObject without touching the network so a
// test can hold an upload in flight and observe concurrent buffer access.
type scriptedHTTPClient struct {
	do func(*http.Request) (*http.Response, error)
}

func (c *scriptedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c.do != nil {
		return c.do(req)
	}
	return putObjectOKResponse(), nil
}

func putObjectOKResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"ETag": []string{`"test-etag"`}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func newMockSink(httpClient *scriptedHTTPClient, batchSize int) *sink {
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("key", "secret", ""),
		HTTPClient:  httpClient,
	})
	return &sink{
		cfg: Config{
			Client:      client,
			Bucket:      "test-bucket",
			ObjectKeyFn: func() string { return "archive/test.ndjson" },
		},
		batchSize: batchSize,
		stop:      make(chan struct{}),
	}
}

func bufferContents(s *sink) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestFlushSerializesWithConcurrentWriteEvent reproduces the race behind
// MASS-0252. drain() used to release mu before PutObject, so a WriteEvent
// batch and a ticker/Flush batch could own disjoint payloads at once. On a
// partial failure restore() would prepend the failed payload ahead of records
// appended in the meantime, reordering or duplicating NDJSON records. The
// fix serializes the whole drain+upload+restore cycle under uploadMu, so a
// concurrent WriteEvent must wait while a flush upload is in flight.
func TestFlushSerializesWithConcurrentWriteEvent(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	release := make(chan struct{})

	httpClient := &scriptedHTTPClient{
		do: func(_ *http.Request) (*http.Response, error) {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return putObjectOKResponse(), nil
		},
	}
	s := newMockSink(httpClient, 100) // large batch: only Flush uploads here
	ctx := context.Background()

	if err := s.WriteEvent(ctx, []byte("rec1\n"), nil); err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	flushDone := make(chan error, 1)
	go func() { flushDone <- s.Flush(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("flush upload did not start")
	}

	writeDone := make(chan error, 1)
	go func() { writeDone <- s.WriteEvent(ctx, []byte("rec2\n"), nil) }()

	// The in-flight flush owns the buffer. A concurrent WriteEvent must not
	// append while it is uploading; before the fix it completed immediately
	// and mutated the buffer behind the uploader's back.
	select {
	case err := <-writeDone:
		t.Fatalf("WriteEvent completed during an in-flight upload (err=%v): buffer=%q", err, bufferContents(s))
	case <-time.After(300 * time.Millisecond):
	}
	if got := bufferContents(s); got != "" {
		t.Fatalf("buffer was mutated during an in-flight upload: %q", got)
	}

	close(release)

	select {
	case err := <-flushDone:
		if err != nil {
			t.Fatalf("flush failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("flush did not complete")
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent write did not complete")
	}

	// rec1 was flushed; rec2 is appended only after that upload finished,
	// so no record is reordered or duplicated.
	if got := bufferContents(s); got != "rec2\n" {
		t.Fatalf("unexpected final buffer: %q", got)
	}
}
