package core

import (
	"sync"
	"testing"
)

// TestResetForTestConcurrentWithIDGeneration is a regression test for the data
// race where ResetForTest swapped the globalIDGen pointer while NewUUIDv7 and
// config defaults read it unsynchronized. Under `go test -race` the original
// implementation reports a race on globalIDGen; with the guarded accessor the
// concurrent swap and reads are safe.
func TestResetForTestConcurrentWithIDGeneration(t *testing.T) {
	const writers = 8
	const iterations = 500

	start := make(chan struct{})
	var wg sync.WaitGroup

	// Readers: direct ID generation plus the config-default capture path.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iterations; j++ {
				_ = NewUUIDv7()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for j := 0; j < iterations; j++ {
			_ = Test() // captures IDGen: getGlobalIDGen()
		}
	}()

	// Writer: repeatedly reset the global generator while readers run.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for j := 0; j < iterations; j++ {
			ResetForTest()
		}
	}()

	close(start)
	wg.Wait()

	// The generator must still produce valid IDs after all the resets.
	id := NewUUIDv7()
	if len(id) != 36 {
		t.Fatalf("NewUUIDv7 returned %q after concurrent resets, want 36-char UUID", id)
	}
}
