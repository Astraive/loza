package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// KeyRateLimiter provides per-key rate limiting using token bucket algorithm.
// It supports both request-level (requests/min) and event-level (events/min) limits.
type KeyRateLimiter struct {
	requestLimiters map[string]*rate.Limiter
	eventLimiters   map[string]*rate.Limiter
	// requestLimits/eventLimits record the rpm/epm a limiter was built for.
	// They let us detect a changed per-key limit and rebuild the limiter so a
	// new limit takes effect even for keys that are actively being used.
	requestLimits map[string]int
	eventLimits   map[string]int
	lastSeen      map[string]time.Time
	mu            sync.RWMutex
	stopCh        chan struct{}
	stopOnce      sync.Once
}

// NewKeyRateLimiter creates a new per-key rate limiter.
func NewKeyRateLimiter() *KeyRateLimiter {
	rl := &KeyRateLimiter{
		requestLimiters: make(map[string]*rate.Limiter),
		eventLimiters:   make(map[string]*rate.Limiter),
		requestLimits:   make(map[string]int),
		eventLimits:     make(map[string]int),
		lastSeen:        make(map[string]time.Time),
		stopCh:          make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Close stops the background cleanup goroutine.
func (rl *KeyRateLimiter) Close() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// cleanupLoop evicts entries not seen in the last 30 minutes.
func (rl *KeyRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-30 * time.Minute)
			for k, t := range rl.lastSeen {
				if t.Before(cutoff) {
					delete(rl.requestLimiters, k)
					delete(rl.eventLimiters, k)
					delete(rl.requestLimits, k)
					delete(rl.eventLimits, k)
					delete(rl.lastSeen, k)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}

// AllowRequest checks if a request is allowed for the given key.
// rpm is the max requests per minute.
func (rl *KeyRateLimiter) AllowRequest(keyID string, rpm int) bool {
	if rpm <= 0 {
		return true // no limit
	}

	limiter := rl.getOrCreateRequestLimiter(keyID, rpm)
	rl.touchLastSeen(keyID)
	return limiter.Allow()
}

// AllowEvents checks if the given number of events is allowed for the key.
// epm is the max events per minute. count is the number of events in this request.
// Uses ReserveN for atomic batch check — no partial token consumption on rejection.
func (rl *KeyRateLimiter) AllowEvents(keyID string, epm int, count int) bool {
	if epm <= 0 {
		return true // no limit
	}
	if count <= 0 {
		count = 1
	}

	limiter := rl.getOrCreateEventLimiter(keyID, epm)
	rl.touchLastSeen(keyID)

	// Atomic reservation: either all tokens are available or none are consumed.
	r := limiter.ReserveN(time.Now(), count)
	if !r.OK() {
		return false
	}
	if d := r.Delay(); d > 0 {
		r.Cancel() // Cancel the reservation since we're rejecting
		return false
	}
	return true
}

// touchLastSeen updates the last-access time for a key.
func (rl *KeyRateLimiter) touchLastSeen(keyID string) {
	rl.mu.Lock()
	rl.lastSeen[keyID] = time.Now()
	rl.mu.Unlock()
}

func (rl *KeyRateLimiter) getOrCreateRequestLimiter(keyID string, rpm int) *rate.Limiter {
	rl.mu.RLock()
	limiter, ok := rl.requestLimiters[keyID]
	configured := rl.requestLimits[keyID]
	rl.mu.RUnlock()
	if ok && configured == rpm {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock. Reuse the limiter only when the
	// configured rpm still matches; otherwise rebuild it so the new limit
	// takes effect even for a key that is actively being used.
	if limiter, ok := rl.requestLimiters[keyID]; ok && rl.requestLimits[keyID] == rpm {
		return limiter
	}

	// Convert rpm to rate per second
	rps := rate.Limit(float64(rpm) / 60.0)
	limiter = rate.NewLimiter(rps, rpm/10+1) // burst = 10% of rpm + 1
	rl.requestLimiters[keyID] = limiter
	rl.requestLimits[keyID] = rpm
	rl.lastSeen[keyID] = time.Now()
	return limiter
}

func (rl *KeyRateLimiter) getOrCreateEventLimiter(keyID string, epm int) *rate.Limiter {
	rl.mu.RLock()
	limiter, ok := rl.eventLimiters[keyID]
	configured := rl.eventLimits[keyID]
	rl.mu.RUnlock()
	if ok && configured == epm {
		return limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if limiter, ok := rl.eventLimiters[keyID]; ok && rl.eventLimits[keyID] == epm {
		return limiter
	}

	eps := rate.Limit(float64(epm) / 60.0)
	// A single ingest request may carry up to a full minute's worth of events
	// (subject to collector.max_events_per_request), so the bucket must be able
	// to hold an entire legitimate batch. With a burst of epm/10+1, ReserveN
	// rejects any count larger than the burst outright, which permanently
	// blocks large batches even when the key is far under its per-minute quota.
	// Sizing the burst to epm keeps the same per-minute budget while allowing
	// any batch that fits within it; batches larger than epm are still rejected.
	limiter = rate.NewLimiter(eps, epm)
	rl.eventLimiters[keyID] = limiter
	rl.eventLimits[keyID] = epm
	rl.lastSeen[keyID] = time.Now()
	return limiter
}
