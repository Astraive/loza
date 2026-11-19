package kafka

import (
	"context"
	"testing"
	"time"
)

func TestNewRejectsInvalidAcks(t *testing.T) {
	_, err := New(Config{
		Brokers: []string{"127.0.0.1:9092"},
		Topic:   "events",
		Acks:    "invalid",
	})
	if err == nil {
		t.Fatal("expected invalid acks error")
	}
}

// Regression test for #499: the Kafka sink must accept idempotence together
// with a non-"all" acks mode instead of failing in kgo.NewClient.
func TestNewAcceptsIdempotenceWithNonAllAcks(t *testing.T) {
	for _, acks := range []string{"0", "1"} {
		t.Run("acks="+acks, func(t *testing.T) {
			sink, err := New(Config{
				Brokers:           []string{"127.0.0.1:9092"},
				Topic:             "events",
				Acks:              acks,
				EnableIdempotence: true,
			})
			if err != nil {
				t.Fatalf("New with idempotence and acks=%q: %v", acks, err)
			}
			if err := sink.Close(context.TODO()); err != nil {
				t.Fatalf("close sink: %v", err)
			}
		})
	}
}

func TestNewAcceptsProducerOptions(t *testing.T) {
	sink, err := New(Config{
		Brokers:           []string{"127.0.0.1:9092"},
		Topic:             "events",
		Acks:              "all",
		RequestTimeout:    2 * time.Second,
		EnableIdempotence: true,
		MaxRetries:        3,
		RetryBackoff:      10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new kafka sink: %v", err)
	}
	if err := sink.Close(context.TODO()); err != nil {
		t.Fatalf("close kafka sink: %v", err)
	}
}

// TestCappedRetryBackoffGrowsAndCaps reproduces the reported defect: the
// backoff must be derived from the retry number, not return a constant value.
func TestCappedRetryBackoffGrowsAndCaps(t *testing.T) {
	base := 250 * time.Millisecond
	max := base * maxRetryBackoffFactor
	for _, tt := range []struct {
		n    int
		want time.Duration
	}{
		{-1, base},
		{0, base},
		{1, 2 * base},
		{2, 4 * base},
		{3, 8 * base},
		{4, 16 * base},
		{5, max},
		{20, max},
	} {
		if got := cappedRetryBackoff(base, tt.n); got != tt.want {
			t.Errorf("cappedRetryBackoff(%s, %d) = %s, want %s", base, tt.n, got, tt.want)
		}
	}
}

// TestRetryBackoffFnJittersAndGrows verifies the function handed to franz-go
// both varies between calls (breaking lockstep retries) and grows with the
// retry number.
func TestRetryBackoffFnJittersAndGrows(t *testing.T) {
	base := time.Second
	fn := retryBackoffFn(base)

	// Jitter: repeated calls at the same retry number produce different delays.
	seen := map[time.Duration]bool{}
	for i := 0; i < 100; i++ {
		seen[fn(3)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected jittered delays at retry 3, got single value %v", fn(3))
	}

	// Equal jitter keeps delay for retry n within [d(n-1), d(n)], so a higher
	// retry never waits less than the maximum of the previous one.
	prev := cappedRetryBackoff(base, 0)
	for n := 1; n <= 4; n++ {
		cur := cappedRetryBackoff(base, n)
		for i := 0; i < 100; i++ {
			got := fn(n)
			if got < prev || got > cur {
				t.Fatalf("retry %d delay %s outside expected [%s, %s]", n, got, prev, cur)
			}
		}
		prev = cur
	}
}
