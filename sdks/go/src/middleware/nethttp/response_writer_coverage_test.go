package nethttp

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type maskFH struct{ *baseTestWriter }
func (*maskFH) Flush() {}
func (*maskFH) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }

type maskFP struct{ *baseTestWriter }
func (*maskFP) Flush() {}
func (*maskFP) Push(string, *http.PushOptions) error { return nil }

type maskFR struct{ *baseTestWriter }
func (*maskFR) Flush() {}
func (*maskFR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

type maskHP struct{ *baseTestWriter }
func (*maskHP) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }
func (*maskHP) Push(string, *http.PushOptions) error { return nil }

type maskHR struct{ *baseTestWriter }
func (*maskHR) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }
func (*maskHR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

type maskPR struct{ *baseTestWriter }
func (*maskPR) Push(string, *http.PushOptions) error { return nil }
func (*maskPR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

type maskFHP struct{ *baseTestWriter }
func (*maskFHP) Flush() {}
func (*maskFHP) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }
func (*maskFHP) Push(string, *http.PushOptions) error { return nil }

type maskFHR struct{ *baseTestWriter }
func (*maskFHR) Flush() {}
func (*maskFHR) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }
func (*maskFHR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

type maskFPR struct{ *baseTestWriter }
func (*maskFPR) Flush() {}
func (*maskFPR) Push(string, *http.PushOptions) error { return nil }
func (*maskFPR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

type inflatedReaderFrom struct{ *baseTestWriter }
func (*inflatedReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	_, _ = io.Copy(io.Discard, r)
	return 2, nil
}

// shrinkingReaderFrom models a writer that consumes the whole source but
// writes fewer bytes to the response (e.g. a compressing writer). The bytes
// read from the source (cr.n) therefore exceed the bytes actually written (n).
type shrinkingReaderFrom struct{ *baseTestWriter }
func (*shrinkingReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	_, _ = io.Copy(io.Discard, r)
	return 3, nil
}

// zeroReportingReaderFrom models a ReaderFrom implementation that consumes the
// source but does not report a byte count, exercising the fallback to cr.n.
type zeroReportingReaderFrom struct{ *baseTestWriter }
func (*zeroReportingReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	_, _ = io.Copy(io.Discard, r)
	return 0, nil
}

type maskHPR struct{ *baseTestWriter }
func (*maskHPR) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, context.Canceled }
func (*maskHPR) Push(string, *http.PushOptions) error { return nil }
func (*maskHPR) ReadFrom(r io.Reader) (int64, error) { return io.Copy(io.Discard, r) }

func TestResponseWriterCoversEveryOptionalInterfaceCombination(t *testing.T) {
	cases := []struct {
		name string
		writer http.ResponseWriter
		mask int
	}{
		{"fh", &maskFH{newBaseTestWriter()}, optionalFlusher | optionalHijacker},
		{"fp", &maskFP{newBaseTestWriter()}, optionalFlusher | optionalPusher},
		{"fr", &maskFR{newBaseTestWriter()}, optionalFlusher | optionalReaderFrom},
		{"hp", &maskHP{newBaseTestWriter()}, optionalHijacker | optionalPusher},
		{"hr", &maskHR{newBaseTestWriter()}, optionalHijacker | optionalReaderFrom},
		{"pr", &maskPR{newBaseTestWriter()}, optionalPusher | optionalReaderFrom},
		{"fhp", &maskFHP{newBaseTestWriter()}, optionalFlusher | optionalHijacker | optionalPusher},
		{"fhr", &maskFHR{newBaseTestWriter()}, optionalFlusher | optionalHijacker | optionalReaderFrom},
		{"fpr", &maskFPR{newBaseTestWriter()}, optionalFlusher | optionalPusher | optionalReaderFrom},
		{"hpr", &maskHPR{newBaseTestWriter()}, optionalHijacker | optionalPusher | optionalReaderFrom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped, state := newResponseWriter(tc.writer)
			if state == nil || wrapped == nil {
				t.Fatal("newResponseWriter returned nil")
			}
			if tc.mask&optionalFlusher != 0 {
				wrapped.(http.Flusher).Flush()
			}
			if tc.mask&optionalHijacker != 0 {
				_, _, _ = wrapped.(http.Hijacker).Hijack()
			}
			if tc.mask&optionalPusher != 0 {
				_ = wrapped.(http.Pusher).Push("/asset", nil)
			}
			if tc.mask&optionalReaderFrom != 0 {
				_, _ = wrapped.(io.ReaderFrom).ReadFrom(strings.NewReader("reader"))
			}
		})
	}
	fullWrapped, _ := newResponseWriter(&fullFeaturedWriter{newBaseTestWriter()})
	fullWrapped.(http.Flusher).Flush()
	_, _, _ = fullWrapped.(http.Hijacker).Hijack()
	_ = fullWrapped.(http.Pusher).Push("/full", nil)
	_, _ = fullWrapped.(io.ReaderFrom).ReadFrom(strings.NewReader("full"))
	baseWrapped := &responseWriter{ResponseWriter: newBaseTestWriter()}
	if _, _, err := baseWrapped.hijack(); err != http.ErrNotSupported {
		t.Fatalf("unsupported hijack error = %v", err)
	}
	if err := baseWrapped.push("/unsupported", nil); err != http.ErrNotSupported {
		t.Fatalf("unsupported push error = %v", err)
	}

	base := &responseWriter{ResponseWriter: &inflatedReaderFrom{newBaseTestWriter()}}
	if n, err := base.readFrom(strings.NewReader("x")); err != nil || n != 2 || base.bytes != 2 {
		t.Fatalf("inflated ReadFrom = n:%d err:%v bytes:%d", n, err, base.bytes)
	}
}

func TestResponseWriterReadFromTracksBytesActuallyWritten(t *testing.T) {
	// The underlying writer reads 10 source bytes but reports writing only 3.
	// response_bytes must reflect the 3 bytes written, not the 10 bytes read.
	base := &responseWriter{ResponseWriter: &shrinkingReaderFrom{newBaseTestWriter()}}
	n, err := base.readFrom(strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("ReadFrom returned n=%d, want 3", n)
	}
	if base.bytes != 3 {
		t.Fatalf("response bytes = %d, want 3 (bytes written, not the 10 bytes read)", base.bytes)
	}
}

func TestResponseWriterReadFromFallsBackToSourceCountWhenWriterReportsZero(t *testing.T) {
	// When the underlying ReaderFrom reports no byte count, the number of
	// bytes read from the source is the best available estimate.
	base := &responseWriter{ResponseWriter: &zeroReportingReaderFrom{newBaseTestWriter()}}
	n, err := base.readFrom(strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("ReadFrom returned n=%d, want 0 (caller contract preserved)", n)
	}
	if base.bytes != 10 {
		t.Fatalf("response bytes = %d, want 10 (fallback to source count)", base.bytes)
// TestReadFromFallbackReachability pins down the contract documented on
// responseWriter.readFrom: the io.Copy fallback is unreachable in production
// because newResponseWriter only exposes ReadFrom for underlying writers that
// implement io.ReaderFrom. It remains available only to direct internal calls.
func TestReadFromFallbackReachability(t *testing.T) {
	const payload = "fallback"

	// Production path: a plain ResponseWriter never yields an io.ReaderFrom
	// wrapper, so the fallback branch cannot be reached through the public value.
	plain := newBaseTestWriter()
	wrapped, _ := newResponseWriter(plain)
	if _, ok := wrapped.(io.ReaderFrom); ok {
		t.Fatal("newResponseWriter exposed io.ReaderFrom for a plain ResponseWriter")
	}

	// Direct internal call: the documented fallback copies via Write and keeps
	// byte accounting and header state correct.
	direct := &responseWriter{ResponseWriter: newBaseTestWriter(), statusCode: http.StatusOK}
	n, err := direct.readFrom(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("fallback readFrom error = %v", err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("fallback readFrom n = %d, want %d", n, len(payload))
	}
	if direct.bytes != len(payload) {
		t.Fatalf("fallback readFrom bytes = %d, want %d", direct.bytes, len(payload))
	}
	if !direct.wroteHeader {
		t.Fatal("fallback readFrom did not mark the header as written")
	}
}
