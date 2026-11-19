package gcs

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"strings"
	"sync"
	"testing"

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
// newFailingFlushSink builds a sink whose final flush fails deterministically
// without any network access: the GCS writer validates the object name
// client-side and rejects the invalid UTF-8 key before issuing a request.
func newFailingFlushSink() *sink {
	invalidKey := string([]byte{0xff, 0xfe})
	s := &sink{
		cfg: Config{
			Client:      &storage.Client{},
			Bucket:      "bucket",
			ObjectKeyFn: func() string { return invalidKey },
		},
		batchSize: 10,
		stop:      make(chan struct{}),
	}
	s.buf.WriteString(`{"event_id":"e1"}` + "\n")
	s.count = 1
	return s
}

func assertFlushError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected Close to return the final flush error, got nil")
	}
	if !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("expected final flush error from the GCS writer, got %v", err)
	}
}

func TestCloseReturnsFlushErrorToEveryCaller(t *testing.T) {
	t.Run("subsequent callers", func(t *testing.T) {
		s := newFailingFlushSink()
		ctx := context.Background()

		first := s.Close(ctx)
		assertFlushError(t, first)

		for i := 0; i < 3; i++ {
			err := s.Close(ctx)
			assertFlushError(t, err)
			if err.Error() != first.Error() {
				t.Fatalf("caller %d got a different error: got %v, want %v", i+1, err, first)
			}
		}
	})

	t.Run("concurrent callers", func(t *testing.T) {
		s := newFailingFlushSink()
		const callers = 8
		start := make(chan struct{})
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = s.Close(context.Background())
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			assertFlushError(t, err)
			if i > 0 && err.Error() != errs[0].Error() {
				t.Fatalf("caller %d got a different error: got %v, want %v", i, err, errs[0])
			}
		}
	})
}
