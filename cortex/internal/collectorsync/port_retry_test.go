package collectorsync

import (
	"errors"
	"fmt"
	"testing"
)

// retryOnPortConflict invokes attempt until it reports that the service has
// bound its port, or until maxAttempts is exhausted. attempt reports bound=false
// when the port selected for the service was claimed by another process before
// the service could bind it (the TOCTOU race that makes freePort flaky). A
// non-nil error aborts the retries immediately. maxAttempts is clamped to at
// least one.
func retryOnPortConflict(maxAttempts int, attempt func(n int) (bound bool, err error)) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for n := 1; n <= maxAttempts; n++ {
		bound, err := attempt(n)
		if err != nil {
			return err
		}
		if bound {
			return nil
		}
		lastErr = fmt.Errorf("port claimed before bind on attempt %d/%d", n, maxAttempts)
	}
	return lastErr
}

// TestRetryOnPortConflictRetriesUntilBound reproduces the reported race: the
// first attempts lose their port to another process (bound=false) and a later
// attempt succeeds. The helper must retry instead of giving up on the first
// conflict.
func TestRetryOnPortConflictRetriesUntilBound(t *testing.T) {
	calls := 0
	err := retryOnPortConflict(3, func(n int) (bool, error) {
		calls++
		return n == 3, nil
	})
	if err != nil {
		t.Fatalf("expected success after retrying, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestRetryOnPortConflictStopsAfterBudget(t *testing.T) {
	calls := 0
	err := retryOnPortConflict(2, func(n int) (bool, error) {
		calls++
		return false, nil // port stolen on every attempt
	})
	if err == nil {
		t.Fatal("expected an error after exhausting the attempt budget")
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls)
	}
}

func TestRetryOnPortConflictPropagatesError(t *testing.T) {
	boom := errors.New("start failed")
	calls := 0
	err := retryOnPortConflict(3, func(n int) (bool, error) {
		calls++
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the start error to be returned, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no retries after a hard error, got %d attempts", calls)
	}
}

func TestRetryOnPortConflictClampsAttempts(t *testing.T) {
	calls := 0
	err := retryOnPortConflict(0, func(n int) (bool, error) {
		calls++
		return true, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("expected exactly one attempt, got calls=%d err=%v", calls, err)
	}
}
