package nethttp

import (
	"io"
	"strings"
	"testing"
)

// reportedCountReaderFrom drains the source (counting a few bytes into the
// countingReader) but reports a much larger number of bytes transferred. This
// exercises the defensive "n > cr.n" branch of readFrom with a >2 GiB count,
// which overflows a 32-bit int but must be preserved in a 64-bit accumulator.
type reportedCountReaderFrom struct {
	*baseTestWriter
	reported int64
}

func (w *reportedCountReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	_, _ = io.Copy(io.Discard, r)
	return w.reported, nil
}

func TestResponseWriterTracksByteCountsBeyondInt32(t *testing.T) {
	const (
		payload  = "abc"               // bytes counted by the countingReader
		drained  = int64(len(payload)) // 3
		huge     = int64(1)<<32 + 1234 // > 2 GiB, overflows a 32-bit int
		reported = huge + drained      // n; n - cr.n == huge
	)

	base := &responseWriter{ResponseWriter: &reportedCountReaderFrom{
		baseTestWriter: newBaseTestWriter(),
		reported:       reported,
	}}

	n, err := base.readFrom(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("readFrom error: %v", err)
	}
	if n != reported {
		t.Fatalf("readFrom n = %d, want %d", n, reported)
	}
	if int64(base.bytes) != reported {
		t.Fatalf("responseWriter.bytes = %d, want %d (64-bit byte counts must not be truncated)", base.bytes, reported)
	}
}
