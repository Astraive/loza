package loadgen

import (
	"encoding/json"
	"math/rand"
	"testing"
)

func TestRandomStringNegativeSizeDoesNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := randomString(rng, -1)
	if got != "" {
		t.Fatalf("randomString(-1) = %q, want empty string", got)
	}
}

func TestRandomStringPositiveSize(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := randomString(rng, 8)
	if len(got) != 8 {
		t.Fatalf("randomString(8) length = %d, want 8", len(got))
	}
}

func TestGenerateEventNegativeBodySizeDoesNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	data, err := GenerateEvent(rng, -1)
	if err != nil {
		t.Fatalf("GenerateEvent with negative body size: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	payload, ok := event["payload"].(string)
	if !ok || payload != "" {
		t.Fatalf("payload = %v, want empty string", event["payload"])
	}
}
