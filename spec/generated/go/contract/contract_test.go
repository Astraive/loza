package contract

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const validSmallEvent = `{"schema_version":"v1","event_version":"v1","event_id":"evt-small","timestamp":"2024-01-01T00:00:00Z","service":"test","event":"small","kind":"event"}`

// oversizedEventLine builds a single valid event record larger than
// bufio.Scanner's default 64 KiB token limit by padding an allowed field.
func oversizedEventLine(eventID string, total int) []byte {
	prefix := fmt.Sprintf(`{"schema_version":"v1","event_version":"v1","event_id":%q,"timestamp":"2024-01-01T00:00:00Z","service":"test","event":"oversized","kind":"event","message":"`, eventID)
	suffix := `"}`
	pad := total - len(prefix) - len(suffix)
	if pad < 0 {
		pad = 0
	}
	return []byte(prefix + strings.Repeat("x", pad) + suffix)
}

func ndjson(lines ...[]byte) []byte {
	return bytes.Join(lines, []byte("\n"))
}

func TestValidateFlexibleJSONBytesOversizedLine(t *testing.T) {
	large := oversizedEventLine("evt-large", 100*1024)
	if len(large) <= 64*1024 {
		t.Fatalf("test fixture is not oversized: %d bytes", len(large))
	}

	payload := ndjson([]byte(validSmallEvent), large)
	if err := ValidateFlexibleJSONBytes(payload, false); err != nil {
		t.Fatalf("expected oversized valid NDJSON record to validate, got %v", err)
	}
}

func TestValidateFlexibleJSONBytesOversizedInvalidLine(t *testing.T) {
	large := oversizedEventLine("evt-large", 100*1024)
	invalid := bytes.Replace(large, []byte(`"event_id":"evt-large",`), nil, 1)
	if len(invalid) == len(large) {
		t.Fatal("failed to build oversized invalid fixture")
	}

	payload := ndjson([]byte(validSmallEvent), invalid)
	err := ValidateFlexibleJSONBytes(payload, false)
	if err == nil {
		t.Fatal("expected invalid oversized NDJSON record to be rejected, not silently skipped")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	for _, ve := range verrs {
		if ve.Code == "missing_event_id" {
			return
		}
	}
	t.Fatalf("expected missing_event_id validation error, got %+v", verrs)
}

func TestValidateFlexibleJSONBytesOversizedMalformedLine(t *testing.T) {
	malformed := append([]byte(`{"event":"`), bytes.Repeat([]byte("x"), 100*1024)...)
	payload := ndjson([]byte(validSmallEvent), malformed)
	if err := ValidateFlexibleJSONBytes(payload, false); err == nil {
		t.Fatal("expected malformed oversized NDJSON record to be rejected, not silently skipped")
	}
}
