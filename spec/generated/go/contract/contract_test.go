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

// assertTooLargeError asserts that an oversized event is rejected by the
// shape-independent per-event byte limit (the MASS-0837 security fix), rather
// than being silently skipped or accepted.
func assertTooLargeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected oversized NDJSON record to be rejected by the per-event size limit")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	for _, ve := range verrs {
		if ve.Code == "too_large" {
			return
		}
	}
	t.Fatalf("expected too_large validation error, got %+v", verrs)
}

func TestValidateFlexibleJSONBytesOversizedLine(t *testing.T) {
	large := oversizedEventLine("evt-large", 100*1024)
	if len(large) <= 64*1024 {
		t.Fatalf("test fixture is not oversized: %d bytes", len(large))
	}

	payload := ndjson([]byte(validSmallEvent), large)
	// Oversized events must be rejected even when otherwise valid; the raw
	// byte size limit is enforced independently of the payload shape.
	assertTooLargeError(t, ValidateFlexibleJSONBytes(payload, false))
}

func TestValidateFlexibleJSONBytesOversizedInvalidLine(t *testing.T) {
	large := oversizedEventLine("evt-large", 100*1024)
	invalid := bytes.Replace(large, []byte(`"event_id":"evt-large",`), nil, 1)
	if len(invalid) == len(large) {
		t.Fatal("failed to build oversized invalid fixture")
	}

	payload := ndjson([]byte(validSmallEvent), invalid)
	// The per-event size check runs before field-level validation, so an
	// oversized line is rejected as too_large regardless of the fact that it
	// is also missing event_id.
	assertTooLargeError(t, ValidateFlexibleJSONBytes(payload, false))
}

func TestValidateFlexibleJSONBytesOversizedMalformedLine(t *testing.T) {
	malformed := append([]byte(`{"event":"`), bytes.Repeat([]byte("x"), 100*1024)...)
	payload := ndjson([]byte(validSmallEvent), malformed)
	if err := ValidateFlexibleJSONBytes(payload, false); err == nil {
		t.Fatal("expected malformed oversized NDJSON record to be rejected, not silently skipped")
	}
}
