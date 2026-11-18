package pool

import (
	"sync"
	"testing"
)

func TestGetReturnsReusableEmptyBuffer(t *testing.T) {
	buf := Get()
	if len(buf) != 0 {
		t.Fatalf("Get length = %d, want 0", len(buf))
	}
	buf = append(buf, "payload"...)
	Put(buf)
	reused := Get()
	if len(reused) != 0 {
		t.Fatalf("reused length = %d, want 0", len(reused))
	}
	Put(reused)
}

func TestGetReturnsZeroedBuffer(t *testing.T) {
	// Use a controlled pool whose New always yields a dirty buffer, so Get must
	// zero the storage itself regardless of whether sync.Pool hands back a
	// recycled buffer or a freshly allocated one.
	orig := BytePool
	defer func() { BytePool = orig }()
	BytePool = &sync.Pool{
		New: func() any {
			b := make([]byte, 0, 16)
			b = append(b, "SENTINEL"...)
			return &b
		},
	}

	buf := Get()
	full := buf[:cap(buf)]
	for i, v := range full {
		if v != 0 {
			t.Fatalf("Get byte %d = %q, want 0 (stale data past len)", i, v)
		}
	}
	Put(buf)
}

func TestPutDiscardsOversizedBuffer(t *testing.T) {
	Put(make([]byte, maxBufSize+1))
	buf := Get()
	if cap(buf) > maxBufSize {
		t.Fatalf("Get returned oversized capacity %d", cap(buf))
	}
	Put(buf)
}
