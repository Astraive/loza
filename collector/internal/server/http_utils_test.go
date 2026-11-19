package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNDJSONEventsBodyLimit(t *testing.T) {
	const max = int64(8)

	tests := []struct {
		name       string
		input      string
		wantEvents [][]byte
		wantTooBig bool
	}{
		{
			name:       "line of exactly maxBytes is accepted",
			input:      strings.Repeat("x", int(max)),
			wantEvents: [][]byte{[]byte(strings.Repeat("x", int(max)))},
		},
		{
			name:       "body of exactly maxBytes split across lines is accepted",
			input:      "abc\n1234",
			wantEvents: [][]byte{[]byte("abc"), []byte("1234")},
		},
		{
			name:       "maxBytes+1 in one line is rejected",
			input:      strings.Repeat("x", int(max)+1),
			wantTooBig: true,
		},
		{
			name:       "maxBytes+1 split across lines is rejected",
			input:      "ab\ncd\nef\ng",
			wantTooBig: true,
		},
		{
			name:       "trailing newline past maxBytes is rejected",
			input:      strings.Repeat("x", int(max)) + "\n",
			wantTooBig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := parseNDJSONEvents(strings.NewReader(tt.input), max)
			if tt.wantTooBig {
				require.Error(t, err)
				assert.True(t, errors.Is(err, errPayloadTooLarge), "want payload-too-large error, got %v", err)
				assert.Nil(t, events)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantEvents, events)
		})
	}
}

// truncatingReader returns some body bytes and then a short-body error, as a
// real net/http request body does when Content-Length exceeds the bytes sent.
type truncatingReader struct {
	data []byte
	read bool
}

func (r *truncatingReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.ErrUnexpectedEOF
	}
	r.read = true
	return copy(p, r.data), nil
}

func TestParseNDJSONEventsTruncationIsNotPayloadTooLarge(t *testing.T) {
	events, err := parseNDJSONEvents(&truncatingReader{data: []byte("abc\n")}, 8)
	assert.Equal(t, [][]byte{[]byte("abc")}, events)
	require.Error(t, err)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.False(t, errors.Is(err, errPayloadTooLarge), "truncation must not be reported as a size violation")
}

func TestHTTPServerIngestBodyLimitStatus(t *testing.T) {
	const max = int64(8)
	srv := NewHTTPServer(HTTPConfig{MaxBodyBytes: max}, &mockState{ready: true, healthy: true})

	t.Run("oversized body returns 413", func(t *testing.T) {
		// ContentLength -1 forces the body through parseNDJSONEvents instead of
		// the Content-Length precheck in handleIngest.
		req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(strings.Repeat("x", int(max)+1)))
		req.ContentLength = -1
		rec := httptest.NewRecorder()

		srv.handleIngest(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Equal(t, "Payload Too Large\n", rec.Body.String())
	})

	t.Run("truncated body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/ingest", &truncatingReader{data: []byte("abc\n")})
		rec := httptest.NewRecorder()

		srv.handleIngest(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "unexpected EOF")
		assert.NotContains(t, rec.Body.String(), "payload exceeds max_body_bytes")
	})
}
