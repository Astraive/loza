package eventbus

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

// errReader simulates an RNG failure by always returning an error.
type errReader struct{ err error }

func (r errReader) Read(_ []byte) (int, error) { return 0, r.err }

// recordingBus records Publish calls so tests can assert nothing is published
// when ID generation fails.
type recordingBus struct {
	publishes int
}

func (b *recordingBus) Publish(_ context.Context, _ string, _ []Envelope) error {
	b.publishes++
	return nil
}
func (b *recordingBus) Subscribe(_ context.Context, _ string, _ string, _ Handler) error {
	return nil
}
func (b *recordingBus) Close(_ context.Context) error { return nil }
func (b *recordingBus) Health(_ context.Context) Health {
	return Health{OK: true}
}

func TestGenerateIDPropagatesRNGError(t *testing.T) {
	rngErr := errors.New("simulated rng failure")

	id, err := generateID(errReader{err: rngErr})
	if err == nil {
		t.Fatalf("generateID() returned id %q with nil error on RNG failure; want error", id)
	}
	if !errors.Is(err, rngErr) {
		t.Errorf("generateID() error = %v; want wrapped rng error %v", err, rngErr)
	}
	if id != "" {
		t.Errorf("generateID() id = %q on RNG failure; want empty id", id)
	}
}

func TestGenerateIDProducesUniqueNonZeroIDs(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id, err := generateID(rand.Reader)
		if err != nil {
			t.Fatalf("generateID() unexpected error: %v", err)
		}
		if len(id) != 32 {
			t.Fatalf("generateID() id length = %d; want 32", len(id))
		}
		if id == "00000000000000000000000000000000" {
			t.Fatal("generateID() returned a zero-filled id")
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("generateID() returned duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestWriteEventPropagatesRNGError(t *testing.T) {
	bus := &recordingBus{}
	sink := &SinkAdapter{bus: bus, topic: "test", idReader: errReader{err: errors.New("rng down")}}

	err := sink.WriteEvent(context.Background(), []byte(`{}`), &collectorevent.Event{})
	if err == nil {
		t.Fatal("WriteEvent() returned nil error on RNG failure; want error")
	}
	if bus.publishes != 0 {
		t.Errorf("WriteEvent() published %d envelopes on RNG failure; want 0", bus.publishes)
	}
}

func TestWriteBatchPropagatesRNGError(t *testing.T) {
	bus := &recordingBus{}
	sink := &SinkAdapter{bus: bus, topic: "test", idReader: errReader{err: errors.New("rng down")}}

	err := sink.WriteBatch(context.Background(), [][]byte{[]byte(`{}`), []byte(`{}`)})
	if err == nil {
		t.Fatal("WriteBatch() returned nil error on RNG failure; want error")
	}
	if bus.publishes != 0 {
		t.Errorf("WriteBatch() published %d times on RNG failure; want 0", bus.publishes)
	}
}
