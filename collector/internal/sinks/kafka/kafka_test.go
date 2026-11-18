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
