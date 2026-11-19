package errors

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestRetryWithBackoffSucceedsAfterRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := RetryWithBackoff(ctx, 3, time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("fail")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestNextBackoffDelayDoublesAndCaps(t *testing.T) {
	tests := []struct {
		name  string
		delay time.Duration
		want  time.Duration
	}{
		{"one nanosecond", 1, 2},
		{"one second", time.Second, 2 * time.Second},
		{"just below half cap", maxBackoffDelay/2 - 1, maxBackoffDelay - 2},
		{"at half cap", maxBackoffDelay / 2, maxBackoffDelay},
		{"above half cap", 20 * time.Second, maxBackoffDelay},
		{"at cap", maxBackoffDelay, maxBackoffDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextBackoffDelay(tt.delay); got != tt.want {
				t.Fatalf("nextBackoffDelay(%v) = %v, want %v", tt.delay, got, tt.want)
			}
		})
	}
}

func TestNextBackoffDelayDoesNotOverflow(t *testing.T) {
	largest := time.Duration(math.MaxInt64)
	for _, delay := range []time.Duration{1 << 62, largest - 1, largest} {
		got := nextBackoffDelay(delay)
		if got <= 0 || got > maxBackoffDelay {
			t.Fatalf("nextBackoffDelay(%d) = %d; want a positive delay capped at %v", delay, got, maxBackoffDelay)
		}
	}
}

func TestBackoffDelaySequenceStaysCapped(t *testing.T) {
	delay := time.Nanosecond
	for i := 0; i < 64; i++ {
		delay = nextBackoffDelay(delay)
		if delay <= 0 || delay > maxBackoffDelay {
			t.Fatalf("delay at step %d = %v; want a positive delay capped at %v", i, delay, maxBackoffDelay)
		}
	}
}

func TestRetryWithBackoffHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RetryWithBackoff(ctx, 3, time.Millisecond, func() error {
		return fmt.Errorf("fail")
	})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTimeoutHelpers(t *testing.T) {
	err := NewTimeoutError(true, context.DeadlineExceeded)
	if !IsTimeout(err) {
		t.Fatal("expected timeout error")
	}
	if IsRetryable(context.Canceled) {
		t.Fatal("context canceled should not be retryable")
	}
	if !IsRetryable(fmt.Errorf("other")) {
		t.Fatal("generic errors should be retryable")
	}
}

func TestIsTimeoutWrapped(t *testing.T) {
	cause := NewTimeoutError(true, context.DeadlineExceeded)

	wrapped := fmt.Errorf("wrapped: %w", cause)
	if !IsTimeout(wrapped) {
		t.Fatal("expected wrapped timeout error to be detected")
	}

	doubleWrapped := fmt.Errorf("outer: %w", wrapped)
	if !IsTimeout(doubleWrapped) {
		t.Fatal("expected doubly wrapped timeout error to be detected")
	}

	if IsTimeout(fmt.Errorf("wrapped: %w", fmt.Errorf("other"))) {
		t.Fatal("non-timeout error should not be detected as a timeout")
	}
}

func TestIsRetryableWrappedContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"wrapped canceled", fmt.Errorf("op: %w", context.Canceled)},
		{"wrapped deadline", fmt.Errorf("op: %w", context.DeadlineExceeded)},
		{"double wrapped canceled", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.Canceled))},
		{"timeout error wrapping canceled", NewTimeoutError(true, context.Canceled)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsRetryable(tt.err) {
				t.Fatalf("wrapped context error %v should not be retryable", tt.err)
			}
		})
	}
}
