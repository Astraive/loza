package core

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestFinishCustomAttrsAtomicWithStateCommit is a regression test for MASS-0421.
//
// finish/finishWithError used to commit state=Finished under the mutex, unlock,
// and then call AddAttrs(custom) in a second lock acquisition. A concurrent
// emit could transition the event to emitted in that window, making AddAttrs
// fail ensureMutableLocked: the custom attrs were dropped and finish returned
// EventClosedError even though it had already committed the finished state.
//
// The test races the finish against a goroutine that flips the event to emitted
// as soon as the finish timestamp becomes visible. A large batch of custom
// attrs widens the vulnerable post-unlock window in the buggy implementation so
// the interleaving is reliably reproduced. The invariant checked below holds
// only when the state commit and the custom-attr append happen atomically.
func TestFinishCustomAttrsAtomicWithStateCommit(t *testing.T) {
	const markerKey = "custom.mass0421"
	const fillerCount = 50000
	const iterations = 10

	tests := []struct {
		name string
		run  func(*Event, []Attr) error
	}{
		{
			name: "finish",
			run: func(ev *Event, attrs []Attr) error {
				return ev.finish(time.Now(), "success", attrs)
			},
		},
		{
			name: "finishWithError",
			run: func(ev *Event, attrs []Attr) error {
				return ev.finishWithError(time.Now(), errors.New("boom"), nil, attrs)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for iter := 0; iter < iterations; iter++ {
				ev := &Event{StartedAt: time.Now()}

				attrs := make([]Attr, 0, fillerCount+1)
				attrs = append(attrs, String(markerKey, "present"))
				for i := 0; i < fillerCount; i++ {
					attrs = append(attrs, String("filler.attr", "x"))
				}

				stop := make(chan struct{})
				done := make(chan struct{})
				go func() {
					defer close(done)
					for {
						select {
						case <-stop:
							return
						default:
						}
						ev.MuLock()
						if !ev.FinishedAt.IsZero() && ev.state == EventStateFinished {
							// Simulate a concurrent Emit winning the race after
							// finish committed but before it recorded custom attrs.
							ev.setStateLocked(EventStateEmitted)
							ev.MuUnlock()
							return
						}
						ev.MuUnlock()
						runtime.Gosched()
					}
				}()

				err := tc.run(ev, attrs)
				close(stop)
				<-done

				// If the finish committed (timestamp set), it must have returned
				// success and the custom attrs must be present, regardless of the
				// concurrent emit transition.
				if ev.IsFinished() {
					if err != nil {
						t.Fatalf("iter %d: finish committed state but returned error: %v", iter, err)
					}
					found := false
					for _, a := range ev.AttrList() {
						if a.Key == markerKey {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("iter %d: custom attr dropped despite finish committing state", iter)
					}
				}
			}
		})
	}
}
