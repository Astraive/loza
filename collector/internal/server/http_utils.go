package server

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"errors"
	"io"
	"math"
	"strconv"
)

// errPayloadTooLarge is the single signal for a body that exceeds the
// configured max_body_bytes limit.
var errPayloadTooLarge = errors.New("payload exceeds max_body_bytes")

// sizeLimitedReader enforces a hard byte limit on a body. Reads up to the
// limit succeed; the first byte beyond it yields errPayloadTooLarge instead of
// being silently dropped (io.LimitReader) or mistaken for a truncated body.
type sizeLimitedReader struct {
	r         io.Reader
	remaining int64
}

func (l *sizeLimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.remaining <= 0 {
		n, err := l.r.Read(p[:1])
		if n > 0 {
			return n, errPayloadTooLarge
		}
		return n, err
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}

func parseNDJSONEvents(body io.Reader, maxBytes int64) ([][]byte, error) {
	reader := body
	if maxBytes > 0 {
		reader = &sizeLimitedReader{r: body, remaining: maxBytes}
	}
	scanner := bufio.NewScanner(reader)
	if maxBytes > 0 {
		// The reader above is the only body-size limit. Give the scanner ample
		// token space so a line of exactly maxBytes bytes is accepted instead
		// of hitting an independent, off-by-one token cap (bufio.ErrTooLong).
		scanner.Buffer(make([]byte, 0, 64*1024), math.MaxInt)
	}
	var events [][]byte
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		events = append(events, line)
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, errPayloadTooLarge) {
			return nil, err
		}
		return events, err
	}
	return events, nil
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// authorizedCredential reports whether presented satisfies the configured
// legacy header auth. It fails closed: an empty configured header name, empty
// configured secret, or empty presented credential is never a match.
// subtle.ConstantTimeCompare reports two empty inputs as equal, so without
// these guards a request with no credential would be authenticated whenever
// AuthValue is empty (for example when auth is enabled via keys/tokens only).
func authorizedCredential(cfg HTTPConfig, presented string) bool {
	if cfg.AuthHeader == "" || cfg.AuthValue == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(cfg.AuthValue)) == 1
}
