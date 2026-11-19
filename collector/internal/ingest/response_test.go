package ingest

import (
	"encoding/json"
	"testing"
)

// TestEventAckIndexAlwaysSerialized is a regression test for the ack index
// being dropped when it is zero. EventAck.Index must be emitted for every ack,
// just like EventError.Index, so clients can position acks by index even for
// the first event in a batch.
func TestEventAckIndexAlwaysSerialized(t *testing.T) {
	tests := []struct {
		name  string
		build func(*Response)
	}{
		{"accepted", func(r *Response) { r.AddAccepted(0, "evt-1") }},
		{"duplicate", func(r *Response) { r.AddDuplicate(0, "evt-1") }},
		{"invalid", func(r *Response) { r.AddInvalid(0, "evt-1", "bad", "bad event") }},
		{"quarantined", func(r *Response) { r.AddQuarantined(0, "evt-1", "bad", "bad event") }},
		{"rejected", func(r *Response) { r.AddRejected(0, "evt-1", "bad", "bad event", true) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Response{}
			tt.build(r)
			r.Finalize()

			b, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			var decoded struct {
				Acks []map[string]any `json:"acks"`
			}
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if len(decoded.Acks) != 1 {
				t.Fatalf("expected 1 ack, got %d: %s", len(decoded.Acks), b)
			}
			idx, ok := decoded.Acks[0]["index"]
			if !ok {
				t.Fatalf("ack is missing index field: %s", b)
			}
			if idx != float64(0) {
				t.Fatalf("expected index 0, got %v: %s", idx, b)
			}
		})
	}
}

// TestEventAckIndexesMatchErrorIndexes verifies that a zero index is emitted
// for both acks and errors, and that non-zero indexes remain correct.
func TestEventAckIndexesMatchErrorIndexes(t *testing.T) {
	r := &Response{}
	r.AddAccepted(0, "evt-0")
	r.AddRejected(1, "evt-1", "bad", "bad event", false)

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var decoded struct {
		Acks   []map[string]any `json:"acks"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(decoded.Acks) != 2 || len(decoded.Errors) != 1 {
		t.Fatalf("unexpected response shape: %s", b)
	}
	wantAck := []float64{0, 1}
	for i, want := range wantAck {
		got, ok := decoded.Acks[i]["index"]
		if !ok || got != want {
			t.Fatalf("ack %d: expected index %v present, got %v (present=%t): %s", i, want, got, ok, b)
		}
	}
	if got, ok := decoded.Errors[0]["index"]; !ok || got != float64(1) {
		t.Fatalf("error 0: expected index 1 present, got %v (present=%t): %s", got, ok, b)
	}
}
