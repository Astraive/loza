package main

import (
	"slices"
	"testing"
)

// clearEventBusKafkaEnv makes both the canonical LOZA_EVENTBUS_KAFKA_* names
// and the deprecated LOZA_KAFKA_* aliases absent for the duration of a test.
// Empty values are ignored by applyEnvOverrides' get helper.
func clearEventBusKafkaEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LOZA_EVENTBUS_KAFKA_BROKERS", "LOZA_EVENTBUS_KAFKA_TOPIC", "LOZA_EVENTBUS_KAFKA_GROUP",
		"LOZA_KAFKA_BROKERS", "LOZA_KAFKA_TOPIC", "LOZA_KAFKA_GROUP",
	} {
		t.Setenv(key, "")
	}
}

func TestApplyEnvOverridesEventBusKafkaCanonicalWins(t *testing.T) {
	clearEventBusKafkaEnv(t)
	t.Setenv("LOZA_EVENTBUS_KAFKA_BROKERS", "canonical-a:9092, canonical-b:9092")
	t.Setenv("LOZA_EVENTBUS_KAFKA_TOPIC", "canonical-topic")
	t.Setenv("LOZA_EVENTBUS_KAFKA_GROUP", "canonical-group")
	t.Setenv("LOZA_KAFKA_BROKERS", "alias-a:9092")
	t.Setenv("LOZA_KAFKA_TOPIC", "alias-topic")
	t.Setenv("LOZA_KAFKA_GROUP", "alias-group")

	fc, err := defaultFileConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyEnvOverrides(&fc); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if want := []string{"canonical-a:9092", "canonical-b:9092"}; !slices.Equal(fc.EventBus.Kafka.Brokers, want) {
		t.Errorf("brokers = %#v, want %#v (canonical must win)", fc.EventBus.Kafka.Brokers, want)
	}
	if fc.EventBus.Kafka.Topic != "canonical-topic" {
		t.Errorf("topic = %q, want %q (canonical must win)", fc.EventBus.Kafka.Topic, "canonical-topic")
	}
	if fc.EventBus.Kafka.ConsumerGroup != "canonical-group" {
		t.Errorf("consumer group = %q, want %q (canonical must win)", fc.EventBus.Kafka.ConsumerGroup, "canonical-group")
	}
}

func TestApplyEnvOverridesEventBusKafkaDeprecatedAlias(t *testing.T) {
	clearEventBusKafkaEnv(t)
	t.Setenv("LOZA_KAFKA_BROKERS", "alias-a:9092, alias-b:9092")
	t.Setenv("LOZA_KAFKA_TOPIC", "alias-topic")
	t.Setenv("LOZA_KAFKA_GROUP", "alias-group")

	fc, err := defaultFileConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyEnvOverrides(&fc); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if want := []string{"alias-a:9092", "alias-b:9092"}; !slices.Equal(fc.EventBus.Kafka.Brokers, want) {
		t.Errorf("brokers = %#v, want %#v (alias must remain supported)", fc.EventBus.Kafka.Brokers, want)
	}
	if fc.EventBus.Kafka.Topic != "alias-topic" {
		t.Errorf("topic = %q, want %q (alias must remain supported)", fc.EventBus.Kafka.Topic, "alias-topic")
	}
	if fc.EventBus.Kafka.ConsumerGroup != "alias-group" {
		t.Errorf("consumer group = %q, want %q (alias must remain supported)", fc.EventBus.Kafka.ConsumerGroup, "alias-group")
	}
}
