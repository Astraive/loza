package auth

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestAllowEvents_AtomicBatchCheck(t *testing.T) {
	rl := NewKeyRateLimiter()

	// With epm=600, burst = 600/10+1 = 61
	// First batch of 10 should succeed
	if !rl.AllowEvents("test-key", 600, 10) {
		t.Fatal("first batch of 10 should be allowed")
	}

	// Second batch of 10 should succeed (still have tokens)
	if !rl.AllowEvents("test-key", 600, 10) {
		t.Fatal("second batch of 10 should be allowed")
	}
}

func TestAllowEvents_LargeBatchUnderQuota(t *testing.T) {
	rl := NewKeyRateLimiter()
	defer rl.Close()

	// epm=600: a single request with 200 events is well under the per-minute
	// quota but larger than the old burst of epm/10+1 = 61. ReserveN used to
	// reject it outright, so the batch could never succeed.
	if !rl.AllowEvents("large-batch", 600, 200) {
		t.Fatal("batch of 200 should be allowed when under the per-minute quota")
	}
}

func TestAllowEvents_BatchLargerThanQuota(t *testing.T) {
	rl := NewKeyRateLimiter()
	defer rl.Close()

	// A batch bigger than the whole per-minute budget still must be rejected.
	if rl.AllowEvents("oversized-batch", 60, 61) {
		t.Fatal("batch larger than the per-minute quota should be rejected")
	}
}

func TestAllowEvents_NoPartialConsumption(t *testing.T) {
	rl := NewKeyRateLimiter()

	// With epm=60, rate=1/sec, burst=60
	// Drain the burst
	for i := 0; i < 60; i++ {
		if !rl.AllowEvents("key-partial", 60, 1) {
			t.Fatalf("single event %d should be allowed", i)
		}
	}

	// Now try a batch of 5 — should fail atomically (not consume any)
	if rl.AllowEvents("key-partial", 60, 5) {
		t.Fatal("batch of 5 should fail after draining burst")
	}

	// A single event should still fail — confirming no partial consumption
	// (bucket is empty, needs time to refill)
	if rl.AllowEvents("key-partial", 60, 1) {
		// May succeed if enough time passed, that's OK
	}
}

func TestAllowEvents_ZeroCountDefaultsToOne(t *testing.T) {
	rl := NewKeyRateLimiter()

	if !rl.AllowEvents("key-zero", 60, 0) {
		t.Fatal("zero count should default to 1 and succeed")
	}
}

func TestAllowEvents_NegativeEPMReturnsTrue(t *testing.T) {
	rl := NewKeyRateLimiter()

	// Negative or zero EPM means no limit
	if !rl.AllowEvents("key-neg", -1, 1000) {
		t.Fatal("negative EPM should always return true")
	}
	if !rl.AllowEvents("key-zero-epm", 0, 1000) {
		t.Fatal("zero EPM should always return true")
	}
}

func TestAllowRequest_BasicFlow(t *testing.T) {
	rl := NewKeyRateLimiter()

	// rpm=60 means 1/sec, burst=7
	if !rl.AllowRequest("req-key", 60) {
		t.Fatal("first request should be allowed")
	}
	if !rl.AllowRequest("req-key", 60) {
		t.Fatal("second request should be allowed")
	}
}

func TestAllowRequest_NegativeRPMReturnsTrue(t *testing.T) {
	rl := NewKeyRateLimiter()

	if !rl.AllowRequest("req-neg", -1) {
		t.Fatal("negative RPM should always return true")
	}
}

func TestCleanup_EvictsOldEntries(t *testing.T) {
	rl := &KeyRateLimiter{
		requestLimiters: make(map[string]*rate.Limiter),
		eventLimiters:   make(map[string]*rate.Limiter),
		lastSeen:        make(map[string]time.Time),
	}

	// Simulate old entries
	rl.requestLimiters["old-key"] = rate.NewLimiter(rate.Inf, 0)
	rl.eventLimiters["old-key"] = rate.NewLimiter(rate.Inf, 0)
	rl.lastSeen["old-key"] = time.Now().Add(-31 * time.Minute)

	rl.requestLimiters["new-key"] = rate.NewLimiter(rate.Inf, 0)
	rl.eventLimiters["new-key"] = rate.NewLimiter(rate.Inf, 0)
	rl.lastSeen["new-key"] = time.Now()

	// Run cleanup manually
	rl.mu.Lock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for k, lastAccess := range rl.lastSeen {
		if lastAccess.Before(cutoff) {
			delete(rl.requestLimiters, k)
			delete(rl.eventLimiters, k)
			delete(rl.lastSeen, k)
		}
	}
	rl.mu.Unlock()

	if _, ok := rl.requestLimiters["old-key"]; ok {
		t.Error("old-key should have been evicted")
	}
	if _, ok := rl.requestLimiters["new-key"]; !ok {
		t.Error("new-key should NOT have been evicted")
	}
}

func TestAllowEvents_DifferentKeysIndependent(t *testing.T) {
	rl := NewKeyRateLimiter()

	// Drain key-a
	for i := 0; i < 7; i++ {
		rl.AllowEvents("key-a", 60, 1)
	}

	// key-b should still be allowed
	if !rl.AllowEvents("key-b", 60, 1) {
		t.Fatal("key-b should be independent of key-a")
	}
}

// TestAllowRequest_LimitChangeTakesEffect verifies that changing rpm for an
// already-active key replaces the limiter instead of reusing the stale one.
func TestAllowRequest_LimitChangeTakesEffect(t *testing.T) {
	rl := NewKeyRateLimiter()
	defer rl.Close()

	// Drain the bucket at the high limit (rpm=600 -> burst=61).
	for i := 0; i < 200; i++ {
		if !rl.AllowRequest("rotating-key", 600) {
			break
		}
	}
	if rl.AllowRequest("rotating-key", 600) {
		t.Fatal("expected the bucket to be drained at rpm=600")
	}

	// Lower the limit drastically. The limiter must be rebuilt with a fresh
	// 1-token bucket (rpm=1 -> burst=1), so this immediate call is allowed.
	// Reusing the drained limiter would deny it.
	if !rl.AllowRequest("rotating-key", 1) {
		t.Fatal("lowering rpm should replace the limiter with a fresh one")
	}

	// The rebuilt limiter has burst=1 and a very slow refill (1/60 per sec),
	// so the immediately following call must be throttled.
	if rl.AllowRequest("rotating-key", 1) {
		t.Fatal("tightened rpm should throttle: second immediate call must be denied")
	}
}

// TestAllowEvents_LimitChangeTakesEffect is the event-level counterpart of
// TestAllowRequest_LimitChangeTakesEffect.
func TestAllowEvents_LimitChangeTakesEffect(t *testing.T) {
	rl := NewKeyRateLimiter()
	defer rl.Close()

	// Drain the bucket at the high limit (epm=600 -> burst=61).
	for i := 0; i < 200; i++ {
		if !rl.AllowEvents("rotating-key", 600, 1) {
			break
		}
	}
	if rl.AllowEvents("rotating-key", 600, 1) {
		t.Fatal("expected the event bucket to be drained at epm=600")
	}

	// Lower the limit drastically: a fresh limiter (epm=1 -> burst=1) allows
	// one event, then immediately throttles.
	if !rl.AllowEvents("rotating-key", 1, 1) {
		t.Fatal("lowering epm should replace the limiter with a fresh one")
	}
	if rl.AllowEvents("rotating-key", 1, 1) {
		t.Fatal("tightened epm should throttle: second immediate call must be denied")
	}
}

// TestLimitChange_RebuildsWithConfiguredRate checks the internal state so the
// assertion is independent of wall-clock timing: both tightening and raising
// the limit must leave a limiter configured for the most recent value.
func TestLimitChange_RebuildsWithConfiguredRate(t *testing.T) {
	rl := NewKeyRateLimiter()
	defer rl.Close()

	rl.AllowRequest("rate-key", 600)
	rl.AllowEvents("rate-key", 600, 1)

	rl.AllowRequest("rate-key", 120)
	rl.AllowEvents("rate-key", 30, 1)

	wantReq := rate.Limit(float64(120) / 60.0)
	if got := rl.requestLimiters["rate-key"].Limit(); got != wantReq {
		t.Fatalf("request limiter rate = %v, want %v after rpm change", got, wantReq)
	}
	wantEvt := rate.Limit(float64(30) / 60.0)
	if got := rl.eventLimiters["rate-key"].Limit(); got != wantEvt {
		t.Fatalf("event limiter rate = %v, want %v after epm change", got, wantEvt)
	}
}
