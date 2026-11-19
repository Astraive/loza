package pool

import "sync"

const (
	defaultBufSize = 2 * 1024  // 2 KB
	maxBufSize     = 64 * 1024 // 64 KB — buffers larger than this are discarded
)

// bytePool is a sync.Pool of []byte buffers. It is deliberately unexported:
// Get and Put are the only accessors, so callers cannot insert a foreign value
// and break the *[]byte invariant that Get relies on.
var bytePool = &sync.Pool{
	New: func() any {
		buf := make([]byte, 0, defaultBufSize)
		return &buf
	},
}

// Get returns a zeroed buffer from the pool.
func Get() []byte {
	bp, ok := bytePool.Get().(*[]byte)
	if !ok {
		// A foreign value must never turn into a panic for callers.
		buf := make([]byte, 0, defaultBufSize)
		return buf
	}
	buf := (*bp)[:0]
	// Resetting the length alone leaves the previous owner's bytes in the
	// backing array, so a caller that reslices past len would read stale data.
	// Clear the retained capacity to honor the zeroed-buffer contract.
	clear(buf[:cap(buf)])
	return buf
}

// Put returns a buffer to the pool. Buffers larger than maxBufSize are dropped
// to prevent long-lived large allocations from staying in the pool.
func Put(buf []byte) {
	if cap(buf) > maxBufSize {
		return
	}
	buf = buf[:0]
	bytePool.Put(&buf)
}
