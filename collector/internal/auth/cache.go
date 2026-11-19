package auth

import (
	"container/list"
	"sync"
	"time"
)

// defaultMaxCacheEntries caps the number of entries held by a MemoryKeyCache.
// It bounds memory use even when callers cache a miss for every distinct key ID
// submitted by unauthenticated clients.
const defaultMaxCacheEntries = 10000

// Fallback TTLs used when a non-positive value is supplied to NewMemoryKeyCache.
// They mirror the collector's configured defaults (5m cache / 30s negative).
const (
	defaultCacheTTL    = 5 * time.Minute
	defaultNegativeTTL = 30 * time.Second
)

type cacheEntry struct {
	record   *KeyRecord
	expires  time.Time
	negative bool          // cache miss entry (short TTL)
	elem     *list.Element // position in the LRU list (front = most recent)
}

// MemoryKeyCache is a bounded, TTL-based in-memory cache for API key lookups.
// When the entry cap is reached, the least recently used entry is evicted on
// insert, so the cache cannot grow without limit from a flood of unknown keys.
type MemoryKeyCache struct {
	entries         map[string]*cacheEntry
	lru             *list.List
	mu              sync.Mutex
	defaultTTL      time.Duration
	negativeTTL     time.Duration
	cleanupInterval time.Duration
	maxEntries      int
	stopCleanup     chan struct{}
	cleanupWG       sync.WaitGroup
}

// NewMemoryKeyCache creates a new cache with the given TTLs and the default
// maximum entry count.
// Positive entries: defaultTTL. Negative entries (misses): negativeTTL.
func NewMemoryKeyCache(defaultTTL, negativeTTL time.Duration) *MemoryKeyCache {
	return NewMemoryKeyCacheWithMaxEntries(defaultTTL, negativeTTL, defaultMaxCacheEntries)
}

// NewMemoryKeyCacheWithMaxEntries creates a new cache with the given TTLs and a
// cap on the number of entries. Inserts beyond maxEntries evict the least
// recently used entry. A maxEntries <= 0 selects the default cap.
// Non-positive TTLs are clamped to safe defaults; in particular this keeps the
// cleanup interval positive so time.NewTicker cannot panic in the cleanup goroutine.
func NewMemoryKeyCacheWithMaxEntries(defaultTTL, negativeTTL time.Duration, maxEntries int) *MemoryKeyCache {
	if maxEntries <= 0 {
		maxEntries = defaultMaxCacheEntries
	}
	if defaultTTL <= 0 {
		defaultTTL = defaultCacheTTL
	}
	if negativeTTL <= 0 {
		negativeTTL = defaultNegativeTTL
	}

	c := &MemoryKeyCache{
		entries:         make(map[string]*cacheEntry),
		lru:             list.New(),
		defaultTTL:      defaultTTL,
		negativeTTL:     negativeTTL,
		cleanupInterval: defaultTTL,
		maxEntries:      maxEntries,
		stopCleanup:     make(chan struct{}),
	}
	c.cleanupWG.Add(1)
	go c.cleanupLoop()
	return c
}

// Get retrieves a key record from the cache.
// Returns (record, true) on positive hit, (nil, false) on miss or negative cache hit.
func (c *MemoryKeyCache) Get(keyID string) (*KeyRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[keyID]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	c.lru.MoveToFront(entry.elem)
	if entry.negative {
		return nil, false // negative cache hit
	}
	return entry.record, true
}

// Set stores a key record in the cache.
func (c *MemoryKeyCache) Set(keyID string, record *KeyRecord, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl <= 0 {
		ttl = c.defaultTTL
	}
	c.store(keyID, &cacheEntry{
		record:  record,
		expires: time.Now().Add(ttl),
	})
}

// SetNegative stores a negative cache entry (key not found).
func (c *MemoryKeyCache) SetNegative(keyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.store(keyID, &cacheEntry{
		expires:  time.Now().Add(c.negativeTTL),
		negative: true,
	})
}

// store inserts or replaces an entry and enforces the entry cap. It must be
// called with c.mu held.
func (c *MemoryKeyCache) store(keyID string, entry *cacheEntry) {
	if existing, ok := c.entries[keyID]; ok {
		// Preserve the existing LRU element, just refresh the value.
		existing.record = entry.record
		existing.expires = entry.expires
		existing.negative = entry.negative
		c.lru.MoveToFront(existing.elem)
		return
	}

	entry.elem = c.lru.PushFront(keyID)
	c.entries[keyID] = entry

	for c.lru.Len() > c.maxEntries {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		delete(c.entries, oldest.Value.(string))
		c.lru.Remove(oldest)
	}
}

// Invalidate removes a key from the cache.
func (c *MemoryKeyCache) Invalidate(keyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[keyID]; ok {
		c.lru.Remove(entry.elem)
		delete(c.entries, keyID)
	}
}

// Close stops the background cleanup goroutine and waits for it to exit.
func (c *MemoryKeyCache) Close() {
	close(c.stopCleanup)
	c.cleanupWG.Wait()
}

func (c *MemoryKeyCache) cleanupLoop() {
	defer c.cleanupWG.Done()
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.evictExpired()
		case <-c.stopCleanup:
			return
		}
	}
}

func (c *MemoryKeyCache) evictExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.entries {
		if now.After(v.expires) {
			c.lru.Remove(v.elem)
			delete(c.entries, k)
		}
	}
}
