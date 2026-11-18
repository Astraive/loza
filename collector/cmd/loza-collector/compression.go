package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"
)

// Compression writer factories are variables so tests can inject a writer
// whose Close fails, reproducing a footer-write failure deterministically.
var (
	newGzipResponseWriter = func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) }
	newZstdResponseWriter = func(w io.Writer) (io.WriteCloser, error) { return zstd.NewWriter(w) }
)

func withResponseCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		acceptEncoding := strings.ToLower(r.Header.Get("Accept-Encoding"))

		var (
			writer   io.WriteCloser
			encoding string
		)
		// Compressed bytes are staged here instead of being written straight to
		// w. This keeps the response headers uncommitted until the compressed
		// stream (including its footer) has been finalized successfully. Once a
		// handler flushes, the staged bytes are committed and later writes pass
		// through, preserving streaming responses such as /tail.
		staged := &stagedCompressionWriter{underlying: w, statusCode: http.StatusOK}
		switch {
		case strings.Contains(acceptEncoding, "zstd"):
			encoder, err := newZstdResponseWriter(staged)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			writer = encoder
			encoding = "zstd"
		case strings.Contains(acceptEncoding, "gzip"):
			writer = newGzipResponseWriter(staged)
			encoding = "gzip"
		default:
			next.ServeHTTP(w, r)
			return
		}
		staged.encoding = encoding

		cw := &compressedResponseWriter{
			ResponseWriter: w,
			writer:         writer,
			staged:         staged,
		}
		next.ServeHTTP(cw, r)

		// The final frame/footer bytes are emitted by Close. Check that error
		// before committing anything so a failed finalization cannot produce a
		// truncated body advertised with 200 + Content-Encoding.
		if err := writer.Close(); err != nil {
			logJSON("error", "response_compression_close_failed", map[string]any{
				"encoding": encoding,
				"error":    err.Error(),
			})
			if !staged.committed {
				http.Error(w, "compression failure", http.StatusInternalServerError)
			}
			return
		}
		if err := staged.commit(); err != nil {
			logJSON("error", "response_compression_write_failed", map[string]any{
				"encoding": encoding,
				"error":    err.Error(),
			})
		}
	})
}

// stagedCompressionWriter buffers compressed output until the response is
// committed, then forwards writes directly to the underlying writer. This
// lets the middleware defer committing headers until the compressed stream has
// been finalized while still supporting handlers that flush progressively.
type stagedCompressionWriter struct {
	underlying http.ResponseWriter
	buf        bytes.Buffer
	encoding   string
	statusCode int
	committed  bool
}

func (s *stagedCompressionWriter) Write(p []byte) (int, error) {
	if s.committed {
		return s.underlying.Write(p)
	}
	return s.buf.Write(p)
}

func (s *stagedCompressionWriter) commit() error {
	if s.committed {
		return nil
	}
	s.committed = true

	header := s.underlying.Header()
	header.Set("Content-Encoding", s.encoding)
	header.Add("Vary", "Accept-Encoding")
	header.Del("Content-Length")
	s.underlying.WriteHeader(s.statusCode)

	_, err := s.underlying.Write(s.buf.Bytes())
	s.buf.Reset()
	return err
}

type compressedResponseWriter struct {
	http.ResponseWriter
	writer      io.Writer
	staged      *stagedCompressionWriter
	wroteHeader bool
}

func (w *compressedResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.staged.statusCode = statusCode
}

func (w *compressedResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.writer.Write(p)
}

func (w *compressedResponseWriter) Flush() {
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}
	if err := w.staged.commit(); err != nil {
		logJSON("error", "response_compression_flush_failed", map[string]any{"error": err.Error()})
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
