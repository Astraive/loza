package core

import (
	"strings"
	"testing"
)

// ingestEnvelopeWithEvents builds a shape-valid envelope whose events array is
// populated with the supplied raw JSON objects.
func ingestEnvelopeWithEvents(events ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"api_version":"v1","source":{"sdk":"loza-go","version":"0.2.0","service":"checkout"},"events":[`)
	for i, event := range events {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(event)
	}
	b.WriteString("]}")
	return []byte(b.String())
}

func TestValidateIngestEnvelopeBytesStrictRejectsEventWithoutName(t *testing.T) {
	envelope := ingestEnvelopeWithEvents(`{"service":"checkout"}`, `{"event":"checkout.request","service":"checkout"}`)

	if err := ValidateIngestEnvelopeBytes(envelope, false); err != nil {
		t.Fatalf("non-strict validation should accept shape-valid envelope: %v", err)
	}
	if err := ValidateIngestEnvelopeBytes(envelope, true); err == nil {
		t.Fatal("strict validation must reject an event with no 'event' field")
	}
}

func TestValidateIngestEnvelopeBytesStrictRejectsEmptyEventName(t *testing.T) {
	for _, event := range []string{
		`{"event":"","service":"checkout"}`,
		`{"event":"   ","service":"checkout"}`,
	} {
		envelope := ingestEnvelopeWithEvents(event)
		if err := ValidateIngestEnvelopeBytes(envelope, true); err == nil {
			t.Fatalf("strict validation must reject an event with a blank 'event' field: %s", event)
		}
	}
}

func TestValidateIngestEnvelopeBytesStrictAcceptsNamedEvents(t *testing.T) {
	envelope := ingestEnvelopeWithEvents(
		`{"event":"checkout.request","service":"checkout"}`,
		`{"event":"checkout.completed","service":"checkout"}`,
	)
	if err := ValidateIngestEnvelopeBytes(envelope, true); err != nil {
		t.Fatalf("strict validation should accept named events: %v", err)
	}
}
