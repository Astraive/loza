package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// failingCloseWriteCloser simulates a compressor whose final flush/footer
// write fails when Close is called.
type failingCloseWriteCloser struct {
	io.Writer
}

func (f *failingCloseWriteCloser) Close() error {
	return errors.New("simulated footer write failure")
}

func TestResponseCompressionFinalizesBeforeCommittingHeaders(t *testing.T) {
	tests := []struct {
		name     string
		encoding string
		decode   func(t *testing.T, b []byte) string
	}{
		{
			name:     "gzip",
			encoding: "gzip",
			decode: func(t *testing.T, b []byte) string {
				t.Helper()
				r, err := gzip.NewReader(bytes.NewReader(b))
				if err != nil {
					t.Fatalf("gzip new reader: %v", err)
				}
				defer r.Close()
				plain, err := io.ReadAll(r)
				if err != nil {
					t.Fatalf("gzip decompress: %v", err)
				}
				return string(plain)
			},
		},
		{
			name:     "zstd",
			encoding: "zstd",
			decode: func(t *testing.T, b []byte) string {
				t.Helper()
				decoder, err := zstd.NewReader(nil)
				if err != nil {
					t.Fatalf("zstd new reader: %v", err)
				}
				defer decoder.Close()
				plain, err := decoder.DecodeAll(b, nil)
				if err != nil {
					t.Fatalf("zstd decompress: %v", err)
				}
				return string(plain)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const payload = "compressed payload for regression test"

			recorder := httptest.NewRecorder()
			handler := withResponseCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Headers must not advertise compression until the stream has
				// been finalized; at handler time they are still uncommitted.
				if got := w.Header().Get("Content-Encoding"); got != "" {
					t.Errorf("handler observed early Content-Encoding %q", got)
				}
				if _, err := io.WriteString(w, payload); err != nil {
					t.Errorf("write: %v", err)
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", tt.encoding)
			handler.ServeHTTP(recorder, req)

			res := recorder.Result()
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
			}
			if got := res.Header.Get("Content-Encoding"); got != tt.encoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tt.encoding)
			}
			if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if got := tt.decode(t, body); got != payload {
				t.Fatalf("decoded body = %q, want %q", got, payload)
			}
		})
	}
}

func TestResponseCompressionNegotiatesAcceptEncoding(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding []string
		wantEncoding   string
		wantStatus     int
	}{
		{
			name:           "explicitly_rejected_gzip",
			acceptEncoding: []string{"gzip;q=0"},
			wantStatus:     http.StatusOK,
		},
		{
			name:           "rejected_zstd_uses_accepted_gzip",
			acceptEncoding: []string{"zstd;q=0, gzip;q=1"},
			wantEncoding:   "gzip",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "accepted_gzip",
			acceptEncoding: []string{"gzip"},
			wantEncoding:   "gzip",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "identity",
			acceptEncoding: []string{"identity"},
			wantStatus:     http.StatusOK,
		},
		{
			name:       "missing_header",
			wantStatus: http.StatusOK,
		},
		{
			name:           "highest_quality",
			acceptEncoding: []string{"gzip;q=0.4, zstd;q=0.8"},
			wantEncoding:   "zstd",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "wildcard_respects_explicit_rejection",
			acceptEncoding: []string{"*;q=0.5, gzip;q=0"},
			wantEncoding:   "zstd",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "identity_rejected",
			acceptEncoding: []string{"gzip;q=0", "identity;q=0"},
			wantStatus:     http.StatusNotAcceptable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler := withSecurityHeaders(withResponseCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "negotiated payload")
			})))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range tt.acceptEncoding {
				req.Header.Add("Accept-Encoding", value)
			}
			handler.ServeHTTP(recorder, req)

			res := recorder.Result()
			defer res.Body.Close()
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantStatus)
			}
			if got := res.Header.Get("Content-Encoding"); got != tt.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tt.wantEncoding)
			}
			if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

func TestResponseCompressionCloseFailureIsNotCommitted(t *testing.T) {
	var logs bytes.Buffer
	origLogOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(origLogOutput)

	origFactory := newGzipResponseWriter
	newGzipResponseWriter = func(w io.Writer) io.WriteCloser {
		return &failingCloseWriteCloser{Writer: w}
	}
	defer func() { newGzipResponseWriter = origFactory }()

	recorder := httptest.NewRecorder()
	handler := withResponseCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "this compressed body must not be delivered")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(recorder, req)

	res := recorder.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusInternalServerError)
	}
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty on finalization failure", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "compression failure") {
		t.Fatalf("body = %q, want failure response", body)
	}
	if !strings.Contains(logs.String(), "response_compression_close_failed") {
		t.Fatalf("logs = %q, want close failure to be recorded", logs.String())
	}
}

func TestResponseCompressionFlushStillStreams(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := withResponseCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "part1")
		w.(http.Flusher).Flush()
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Errorf("after flush Content-Encoding = %q, want gzip", got)
		}
		_, _ = io.WriteString(w, "part2")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(recorder, req)

	res := recorder.Result()
	defer res.Body.Close()
	if !recorder.Flushed {
		t.Fatal("handler flush did not reach the underlying response writer")
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip new reader: %v", err)
	}
	defer r.Close()
	plain, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip decompress: %v", err)
	}
	if got := string(plain); got != "part1part2" {
		t.Fatalf("decoded body = %q, want %q", got, "part1part2")
	}
}

func TestResponseCompressionPreservesHandlerStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := withResponseCompression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "missing")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(recorder, req)

	res := recorder.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
}
