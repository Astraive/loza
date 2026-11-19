package loza_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/astraive/loza/sdks/go"
)

// kafkaTopicHeader mirrors the routing header KafkaSink is expected to attach.
const kafkaTopicHeader = "X-Loza-Kafka-Topic"

// TestKafkaSinkAttachesTopicHeader is a regression test for the topic
// parameter being silently discarded by KafkaSink. The topic must be attached
// as Kafka routing metadata on the request sent to the collector.
func TestKafkaSinkAttachesTopicHeader(t *testing.T) {
	const topic = "orders-events"

	gotTopic := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTopic <- r.Header.Get(kafkaTopicHeader)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sink, err := loza.KafkaSink(srv.URL+"/events", topic)
	if err != nil {
		t.Fatalf("KafkaSink() error = %v", err)
	}
	defer sink.Close(context.Background())

	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if got := <-gotTopic; got != topic {
		t.Fatalf("%s header = %q, want %q", kafkaTopicHeader, got, topic)
	}
}

// TestKafkaSinkEmptyTopicOmitsHeader documents that an empty topic does not
// attach empty (meaningless) routing metadata.
func TestKafkaSinkEmptyTopicOmitsHeader(t *testing.T) {
	gotTopic := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTopic <- r.Header.Get(kafkaTopicHeader)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sink, err := loza.KafkaSink(srv.URL+"/events", "")
	if err != nil {
		t.Fatalf("KafkaSink() error = %v", err)
	}
	defer sink.Close(context.Background())

	if err := sink.WriteEvent(context.Background(), []byte(`{"event":"test"}`), nil); err != nil {
		t.Fatalf("WriteEvent() error = %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if got := <-gotTopic; got != "" {
		t.Fatalf("%s header = %q, want empty", kafkaTopicHeader, got)
	}
}
