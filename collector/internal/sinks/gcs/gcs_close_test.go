package gcs

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

func newCloseTestSink(t *testing.T) *sink {
	t.Helper()
	gotSink, err := New(Config{
		Client:        &storage.Client{},
		Bucket:        "bucket",
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	s := gotSink.(*sink)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestCloseRetriesFinalFlushAfterFailure(t *testing.T) {
	s := newCloseTestSink(t)

	uploadErr := errors.New("simulated upload failure")
	var attempts atomic.Int32
	var uploaded [][]byte
	s.putObjectFn = func(_ context.Context, payload []byte) error {
		if attempts.Add(1) == 1 {
			return uploadErr
		}
		uploaded = append(uploaded, append([]byte(nil), payload...))
		return nil
	}

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"retry-me"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// The first Close cannot upload; the batch must remain buffered for a retry.
	if err := s.Close(context.Background()); !errors.Is(err, uploadErr) {
		t.Fatalf("first Close error = %v, want %v", err, uploadErr)
	}
	if s.count != 1 || !strings.Contains(s.buf.String(), "retry-me") {
		t.Fatalf("batch was not restored after failed close flush: count=%d buf=%q", s.count, s.buf.String())
	}

	// A second Close must retry the restored batch instead of reporting success.
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("upload attempts = %d, want 2", attempts.Load())
	}
	if len(uploaded) != 1 || !strings.Contains(string(uploaded[0]), "retry-me") {
		t.Fatalf("unexpected uploaded payloads: %q", uploaded)
	}
	if s.count != 0 || s.buf.Len() != 0 {
		t.Fatalf("buffer not drained after successful retry: count=%d buf=%q", s.count, s.buf.String())
	}

	// Once drained, further Close calls are no-ops and never re-upload.
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("third Close: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("upload attempts after drained Close = %d, want 2", attempts.Load())
	}
}

func TestCloseFlushesBufferedDataOnce(t *testing.T) {
	s := newCloseTestSink(t)
	var attempts atomic.Int32
	s.putObjectFn = func(context.Context, []byte) error {
		attempts.Add(1)
		return nil
	}

	if err := s.WriteEvent(context.Background(), []byte(`{"event":"flush-on-close"}`), nil); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("upload attempts = %d, want 1", attempts.Load())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("upload attempts after drained Close = %d, want 1", attempts.Load())
	}
}
