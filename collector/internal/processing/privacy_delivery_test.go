package processing

import (
	"context"
	"strings"
	"sync"
	"testing"

	collectorevent "github.com/astraive/loza/collector/internal/event"
)

// capturingSink records every payload delivered to it so tests can assert that
// sensitive data never reaches a sink.
type capturingSink struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (s *capturingSink) Name() string { return "capture" }

func (s *capturingSink) WriteEvent(_ context.Context, encoded []byte, _ *collectorevent.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads = append(s.payloads, append([]byte(nil), encoded...))
	return nil
}

func (s *capturingSink) Flush(context.Context) error { return nil }
func (s *capturingSink) Close(context.Context) error { return nil }

func (s *capturingSink) Delivered() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.payloads...)
}

// TestProcessRedactionFailureDoesNotDeliverUnredacted verifies that a redaction
// error fails closed in every active privacy mode. The old behavior let "warn"
// mode ship the original (unredacted) payload to sinks when redactPII failed
// (e.g. malformed JSON with ValidateJSONObjects disabled).
func TestProcessRedactionFailureDoesNotDeliverUnredacted(t *testing.T) {
	// Malformed JSON makes redactPII fail at json.Unmarshal. It contains a
	// sensitive value that must never be delivered.
	const rawPayload = `{"email":"alice@example.com",broken`

	for _, mode := range []string{"warn", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			sink := &capturingSink{}
			var warned bool
			p, err := New(Config{
				ValidateJSONObjects: false,
				Privacy: PrivacyConfig{
					Mode:      mode,
					Blocklist: []string{"email"},
				},
				LogFunc: func(level, message string, _ map[string]any) {
					if message == "privacy_redaction_failed" {
						warned = true
					}
				},
			}, sink, nil, nil, nil)
			if err != nil {
				t.Fatalf("new processor: %v", err)
			}
			defer func() { _ = p.Close() }()

			res := p.Process(context.Background(), []byte(rawPayload))

			if !res.Invalid {
				t.Fatalf("expected redaction failure to reject the event, got %+v", res)
			}
			if res.Err == nil || !strings.Contains(res.Err.Error(), "privacy redaction failed") {
				t.Fatalf("expected privacy redaction error, got %v", res.Err)
			}
			if got := len(sink.Delivered()); got != 0 {
				t.Fatalf("expected no payload to be delivered, got %d: %q", got, sink.Delivered())
			}
			if !warned {
				t.Fatalf("expected privacy_redaction_failed warning to be logged")
			}
		})
	}
}

// TestProcessRedactionSuccessStillDelivers is a control case: with valid JSON,
// redaction succeeds and the redacted payload is delivered normally in warn mode.
func TestProcessRedactionSuccessStillDelivers(t *testing.T) {
	sink := &capturingSink{}
	p, err := New(Config{
		ValidateJSONObjects: false,
		Privacy: PrivacyConfig{
			Mode:      "warn",
			Blocklist: []string{"email"},
		},
	}, sink, nil, nil, nil)
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}
	defer func() { _ = p.Close() }()

	res := p.Process(context.Background(), []byte(`{"email":"alice@example.com","name":"Alice"}`))
	if !res.Accepted || res.Err != nil {
		t.Fatalf("expected accepted result, got %+v", res)
	}
	delivered := sink.Delivered()
	if len(delivered) != 1 {
		t.Fatalf("expected one delivered payload, got %d", len(delivered))
	}
	if strings.Contains(string(delivered[0]), "alice@example.com") {
		t.Fatalf("delivered payload still contains unredacted email: %q", delivered[0])
	}
	if !strings.Contains(string(delivered[0]), "[REDACTED]") {
		t.Fatalf("delivered payload was not redacted: %q", delivered[0])
	}
}
