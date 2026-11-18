package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/twmb/franz-go/pkg/kgo"
)

type errTypeA struct{ msg string }

func (e errTypeA) Error() string { return e.msg }

type errTypeB struct{ msg string }

func (e errTypeB) Error() string { return e.msg }

func TestKafkaRequiresBrokers(t *testing.T) {
	ctx := context.Background()
	_, err := New(ctx, eventbus.Config{
		Type:  "kafka",
		Kafka: eventbus.KafkaConfig{},
	})
	if err == nil {
		t.Error("expected error for empty brokers")
	}
}

// Regression test for #499: idempotence combined with a non-"all" acks mode
// must produce a valid franz-go client instead of failing New with
// "idempotency requires acks=all".
func TestProducerOptsIdempotenceWithNonAllAcks(t *testing.T) {
	for _, acks := range []string{"0", "1"} {
		t.Run("acks="+acks, func(t *testing.T) {
			kc := eventbus.KafkaConfig{
				Brokers:           []string{"127.0.0.1:9092"},
				Topic:             "loza.test",
				Acks:              acks,
				EnableIdempotence: true,
			}
			client, err := kgo.NewClient(producerOpts(kc)...)
			if err != nil {
				t.Fatalf("kgo.NewClient with idempotence and acks=%q: %v", acks, err)
			}
			client.Close()
		})
	}
}

func TestKafkaRequiresConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping kafka integration test in short mode")
	}
	ctx := context.Background()
	_, err := New(ctx, eventbus.Config{
		Type: "kafka",
		Kafka: eventbus.KafkaConfig{
			Brokers: []string{"127.0.0.1:9092"},
			Topic:   "loza.test.conn",
		},
	})
	if err != nil {
		t.Skipf("kafka not available: %v", err)
	}
}

func TestKafkaBusContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping kafka integration test in short mode")
	}
	ctx := context.Background()

	// Use unique topic and consumer group per test run to avoid stale group state
	uniqueID := fmt.Sprintf("%d", time.Now().UnixNano())
	topic := "loza.test.contract." + uniqueID
	group := "loza-test-group." + uniqueID

	bus, err := New(ctx, eventbus.Config{
		Type:          "kafka",
		Topic:         topic,
		ConsumerGroup: group,
		Kafka: eventbus.KafkaConfig{
			Brokers:       []string{"127.0.0.1:9092"},
			Topic:         topic,
			ConsumerGroup: group,
			Acks:          "all",
		},
	})
	if err != nil {
		t.Skipf("kafka not available: %v", err)
	}
	defer bus.Close(ctx)

	done := make(chan struct{})
	handler := func(_ context.Context, msg eventbus.Message) error {
		close(done)
		return msg.Ack(ctx)
	}

	if err := bus.Subscribe(ctx, topic, group, handler); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	envelopes := []eventbus.Envelope{
		{ID: "kafka-1", Event: "test.event", Timestamp: time.Now(), Body: []byte(`{"x":1}`)},
	}
	if err := bus.Publish(ctx, topic, envelopes); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestPublishFirstErrHandlesDifferingErrorTypes reproduces the scenario that
// previously panicked in Publish: concurrent produce callbacks reporting
// errors whose concrete (dynamic) types differ. Storing such errors in a
// single atomic.Value makes CompareAndSwap panic with "inconsistently typed
// value into Value". The recorder must accept mixed error types without
// panicking and still return a non-nil error.
func TestPublishFirstErrHandlesDifferingErrorTypes(t *testing.T) {
	errs := []error{
		errTypeA{"a"},                          // distinct struct type
		errTypeB{"b"},                          // another distinct struct type
		errors.New("c"),                        // *errors.errorString
		fmt.Errorf("d: %w", errTypeA{"inner"}), // *fmt.wrapError
	}

	var first firstProduceErr
	const calls = 200
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			first.set(errs[i%len(errs)])
		}(i)
	}
	wg.Wait()

	if got := first.load(); got == nil {
		t.Fatal("expected an error to be recorded")
	}
}

// TestPublishFirstErrKeepsFirstError verifies that nil errors are ignored and
// that the first non-nil error is preserved even when a later error has a
// different concrete type.
func TestPublishFirstErrKeepsFirstError(t *testing.T) {
	var first firstProduceErr
	first.set(nil)
	if got := first.load(); got != nil {
		t.Fatalf("expected nil error, got %v", got)
	}

	want := errTypeA{"first"}
	first.set(want)
	first.set(errTypeB{"second"})
	if got := first.load(); got != want {
		t.Fatalf("first error not preserved: got %v (type %T), want %v", got, got, want)
	}
}
