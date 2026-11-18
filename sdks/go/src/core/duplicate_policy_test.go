package core

import (
	"fmt"
	"sync"
	"testing"
)

// TestApplyDuplicateFieldPolicyNilAndEmpty guards the early-return behavior
// that must be preserved when the empty-attrs check moves under the mutex.
func TestApplyDuplicateFieldPolicyNilAndEmpty(t *testing.T) {
	if err := applyDuplicateFieldPolicy(nil, ErrorOnDuplicate); err != nil {
		t.Fatalf("nil event: %v", err)
	}
	if err := applyDuplicateFieldPolicy(&Event{}, ErrorOnDuplicate); err != nil {
		t.Fatalf("empty event: %v", err)
	}
}

// TestApplyDuplicateFieldPolicyConcurrentAttrs exercises applyDuplicateFieldPolicy
// against AddAttrs, which mutates ev.Attrs under the event mutex. Under -race this
// reproduces the data race caused by reading the ev.Attrs slice header before
// acquiring ev.mu; after the fix every access is made under the lock.
func TestApplyDuplicateFieldPolicyConcurrentAttrs(t *testing.T) {
	ev := &Event{}

	const (
		writers = 4
		readers = 4
		iters   = 250
	)

	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < iters; j++ {
				attrs := []Attr{{Key: fmt.Sprintf("custom.%d.%d", id, j), Value: j}}
				if err := ev.AddAttrs(attrs); err != nil {
					t.Errorf("AddAttrs: %v", err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iters; j++ {
				if err := applyDuplicateFieldPolicy(ev, CanonicalWins); err != nil {
					t.Errorf("applyDuplicateFieldPolicy: %v", err)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
}
