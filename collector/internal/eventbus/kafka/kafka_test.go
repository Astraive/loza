package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
	"github.com/twmb/franz-go/pkg/kgo"
)

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
