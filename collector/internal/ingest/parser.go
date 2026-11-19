package ingest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	speccontract "github.com/astraive/loza/spec/generated/go/contract"
	"github.com/klauspost/compress/zstd"
)

// ErrPayloadTooLarge is returned by ParseEvents when the request body exceeds
// the configured maximum. Callers should detect it with errors.Is rather than
// matching on error-message text, so that rewording the message cannot change
// the HTTP status chosen for an oversized payload.
var ErrPayloadTooLarge = errors.New("payload exceeds max body bytes")

func ParseEvents(r *http.Request, maxBodyBytes int64) ([][]byte, error) {
	defer r.Body.Close()

	reader := io.Reader(r.Body)
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	switch {
	case strings.Contains(encoding, "gzip"):
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	case strings.Contains(encoding, "zstd"):
		decoder, err := zstd.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		reader = decoder
	}

	payload, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBodyBytes {
		return nil, ErrPayloadTooLarge
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, nil
	}

	switch payload[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(payload, &arr); err != nil {
			return nil, err
		}
		out := make([][]byte, 0, len(arr))
		for _, item := range arr {
			out = append(out, bytes.TrimSpace([]byte(item)))
		}
		return out, nil
	case '{':
		// An ingest envelope is identified by its metadata keys, not by whether
		// its "events" list happens to be non-empty. Detect it first so an empty
		// or absent events list yields zero events instead of the envelope
		// document falling through and being treated as a single event.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err == nil {
			_, hasEvents := fields["events"]
			_, hasAPIVersion := fields["api_version"]
			if hasEvents || hasAPIVersion {
				var envelope speccontract.IngestEnvelope
				if err := json.Unmarshal(payload, &envelope); err == nil {
					out := make([][]byte, 0, len(envelope.Events))
					for _, item := range envelope.Events {
						out = append(out, bytes.TrimSpace([]byte(item)))
					}
					return out, nil
				}
			}
		}
		var single json.RawMessage
		if err := json.Unmarshal(payload, &single); err == nil {
			return [][]byte{payload}, nil
		}
		// If object parsing failed, fall back to NDJSON handling.
		if bytes.Contains(payload, []byte{'\n'}) {
			ndjson, err := ParseNDJSON(payload)
			if err != nil {
				return nil, err
			}
			if len(ndjson) > 1 {
				return ndjson, nil
			}
		}
		return nil, fmt.Errorf("invalid JSON object payload")
	default:
		return ParseNDJSON(payload)
	}
}

// ParseNDJSON splits newline-delimited JSON payloads into individual events.
// It returns an error if scanning fails (for example, a token exceeds the
// scanner buffer or the underlying read fails), so callers can distinguish a
// complete parse from a truncated one. Beware: this signature changed from
// [][]byte to ([][]byte, error); update any direct callers accordingly.
func ParseNDJSON(payload []byte) ([][]byte, error) {
	return parseNDJSONReader(bytes.NewReader(payload))
}

func parseNDJSONReader(r io.Reader) ([][]byte, error) {
	scanner := bufio.NewScanner(r)
	buffer := make([]byte, 0, 1024*1024)
	scanner.Buffer(buffer, math.MaxInt32)

	out := make([][]byte, 0, 64)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		copyLine := make([]byte, len(line))
		copy(copyLine, line)
		out = append(out, copyLine)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ingest: read ndjson: %w", err)
	}
	return out, nil
}
