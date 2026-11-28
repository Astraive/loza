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

func preferredResponseEncoding(values []string) string {
	if len(values) == 0 {
		return "identity"
	}

	var (
		gzipQuality, zstdQuality, wildcardQuality, identityQuality int
		hasGzip, hasZstd, hasWildcard, hasIdentity                 bool
	)
	for _, value := range values {
		for value != "" {
			item, rest, _ := strings.Cut(value, ",")
			value = rest
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}

			coding, parameter, hasParameter := strings.Cut(item, ";")
			coding = strings.TrimSpace(coding)
			quality := 1000
			if hasParameter {
				name, value, ok := strings.Cut(parameter, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
					continue
				}
				parsedQuality, ok := parseEncodingQuality(strings.TrimSpace(value))
				if !ok {
					continue
				}
				quality = parsedQuality
			}

			switch {
			case strings.EqualFold(coding, "gzip"):
				if !hasGzip || quality > gzipQuality {
					gzipQuality, hasGzip = quality, true
				}
			case strings.EqualFold(coding, "zstd"):
				if !hasZstd || quality > zstdQuality {
					zstdQuality, hasZstd = quality, true
				}
			case coding == "*":
				if !hasWildcard || quality > wildcardQuality {
					wildcardQuality, hasWildcard = quality, true
				}
			case strings.EqualFold(coding, "identity"):
				if !hasIdentity || quality > identityQuality {
					identityQuality, hasIdentity = quality, true
				}
			}
		}
	}

	if !hasGzip && hasWildcard {
		gzipQuality = wildcardQuality
	}
	if !hasZstd && hasWildcard {
		zstdQuality = wildcardQuality
	}
	if !hasIdentity && hasWildcard && wildcardQuality == 0 {
		identityQuality = 0
	} else if !hasIdentity {
		identityQuality = 1000
	}

	if zstdQuality > 0 && zstdQuality >= gzipQuality && zstdQuality >= identityQuality {
		return "zstd"
	}
	if gzipQuality > 0 && gzipQuality >= identityQuality {
		return "gzip"
	}
	if identityQuality > 0 {
		return "identity"
	}
	return ""
}

func parseEncodingQuality(value string) (int, bool) {
	if value == "" {
		return 0, false
	}

	switch value[0] {
	case '0':
		if len(value) > 1 && value[1] != '.' {
			return 0, false
		}
		fraction := ""
		if len(value) > 1 {
			fraction = value[2:]
		}
		if len(fraction) > 3 {
			return 0, false
		}
		quality := 0
		for i := 0; i < 3; i++ {
			quality *= 10
			if i < len(fraction) {
				digit := fraction[i]
				if digit < '0' || digit > '9' {
					return 0, false
				}
				quality += int(digit - '0')
			}
		}
		return quality, true
	case '1':
		if len(value) > 1 && value[1] != '.' {
			return 0, false
		}
		fraction := ""
		if len(value) > 1 {
			fraction = value[2:]
		}
		if len(fraction) > 3 {
			return 0, false
		}
		for i := range fraction {
			if fraction[i] != '0' {
				return 0, false
			}
		}
		return 1000, true
	default:
		return 0, false
	}
}

func withResponseCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		encoding := preferredResponseEncoding(r.Header.Values("Accept-Encoding"))
		if encoding == "identity" {
			next.ServeHTTP(w, r)
			return
		}
		if encoding == "" {
			http.Error(w, "no acceptable content encoding", http.StatusNotAcceptable)
			return
		}

		var writer io.WriteCloser
		// Compressed bytes are staged here instead of being written straight to
		// w. This keeps the response headers uncommitted until the compressed
		// stream (including its footer) has been finalized successfully. Once a
		// handler flushes, the staged bytes are committed and later writes pass
		// through, preserving streaming responses such as /tail.
		staged := &stagedCompressionWriter{underlying: w, statusCode: http.StatusOK}
		switch encoding {
		case "zstd":
			encoder, err := newZstdResponseWriter(staged)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			writer = encoder
		case "gzip":
			writer = newGzipResponseWriter(staged)
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
