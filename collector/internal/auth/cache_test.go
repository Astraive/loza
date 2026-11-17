package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestMemoryKeyCache_SetAndGet(t *testing.T) {
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()

	record := &KeyRecord{
		KeyID: "k_test123",
		OrgID: "org_1",
		Kind:  KeyKindSecret,
		Roles: []Role{RoleIngestServer},
	}

	cache.Set("k_test123", record, 0)

	got, ok := cache.Get("k_test123")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.KeyID != "k_test123" {
		t.Errorf("keyID = %q, want %q", got.KeyID, "k_test123")
	}
	if got.OrgID != "org_1" {
		t.Errorf("orgID = %q, want %q", got.OrgID, "org_1")
	}
}

func TestMemoryKeyCache_Miss(t *testing.T) {
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestMemoryKeyCache_Expiration(t *testing.T) {
	cache := NewMemoryKeyCache(50*time.Millisecond, 10*time.Millisecond)
	defer cache.Close()

	record := &KeyRecord{KeyID: "k_expire"}
	cache.Set("k_expire", record, 50*time.Millisecond)

	// Should hit immediately
	_, ok := cache.Get("k_expire")
	if !ok {
		t.Fatal("expected cache hit before expiration")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	_, ok = cache.Get("k_expire")
	if ok {
		t.Fatal("expected cache miss after expiration")
	}
}

func TestMemoryKeyCache_NegativeEntry(t *testing.T) {
	cache := NewMemoryKeyCache(10*time.Second, 50*time.Millisecond)
	defer cache.Close()

	cache.SetNegative("k_missing")

	// Negative entry should return miss
	_, ok := cache.Get("k_missing")
	if ok {
		t.Fatal("expected miss for negative cache entry")
	}
}

func TestMemoryKeyCache_Invalidate(t *testing.T) {
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()

	record := &KeyRecord{KeyID: "k_invalidate"}
	cache.Set("k_invalidate", record, 0)

	_, ok := cache.Get("k_invalidate")
	if !ok {
		t.Fatal("expected cache hit before invalidation")
	}

	cache.Invalidate("k_invalidate")

	_, ok = cache.Get("k_invalidate")
	if ok {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestMemoryKeyCache_Overwrite(t *testing.T) {
	cache := NewMemoryKeyCache(10*time.Second, 5*time.Second)
	defer cache.Close()

	cache.Set("k_overwrite", &KeyRecord{KeyID: "k_overwrite", OrgID: "org_1"}, 0)
	cache.Set("k_overwrite", &KeyRecord{KeyID: "k_overwrite", OrgID: "org_2"}, 0)

	got, ok := cache.Get("k_overwrite")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.OrgID != "org_2" {
		t.Errorf("orgID = %q, want %q", got.OrgID, "org_2")
	}
}

// TestMemoryKeyCache_DefaultCapPreventsUnboundedGrowth reproduces the reported
// DoS: unauthenticated lookups with random key IDs call SetNegative for every
// distinct ID. With the default constructor used by the gRPC/HTTP interceptors,
// the cache must stay bounded.
func TestMemoryKeyCache_DefaultCapPreventsUnboundedGrowth(t *testing.T) {
	cache := NewMemoryKeyCache(time.Minute, time.Minute)
	defer cache.Close()

	for i := 0; i < defaultMaxCacheEntries+100; i++ {
		cache.SetNegative(fmt.Sprintf("k_unknown_%d", i))
	}

	if got := len(cache.entries); got > defaultMaxCacheEntries {
		t.Fatalf("negative cache grew to %d entries, exceeding cap of %d", got, defaultMaxCacheEntries)
	}
}

// TestMemoryKeyCache_MaxEntriesBound verifies a caller-supplied cap is enforced.
func TestMemoryKeyCache_MaxEntriesBound(t *testing.T) {
	cache := NewMemoryKeyCacheWithMaxEntries(time.Minute, time.Minute, 8)
	defer cache.Close()

	for i := 0; i < 100; i++ {
		cache.SetNegative(fmt.Sprintf("k_missing_%d", i))
	}

	if got := len(cache.entries); got > 8 {
		t.Fatalf("cache grew to %d entries, want cap of 8", got)
	}
}

// TestMemoryKeyCache_LRUEviction verifies that recently used entries survive
// eviction while the least recently used entry is dropped on insert.
func TestMemoryKeyCache_LRUEviction(t *testing.T) {
	cache := NewMemoryKeyCacheWithMaxEntries(time.Minute, time.Minute, 3)
	defer cache.Close()

	cache.Set("k1", &KeyRecord{KeyID: "k1"}, 0)
	cache.Set("k2", &KeyRecord{KeyID: "k2"}, 0)
	cache.Set("k3", &KeyRecord{KeyID: "k3"}, 0)

	// Touch k1 so k2 becomes the least recently used entry.
	if _, ok := cache.Get("k1"); !ok {
		t.Fatal("expected hit for k1")
	}

	// Inserting a fourth entry must evict k2.
	cache.Set("k4", &KeyRecord{KeyID: "k4"}, 0)

	if _, ok := cache.Get("k2"); ok {
		t.Fatal("expected least recently used entry k2 to be evicted")
	}
	for _, id := range []string{"k1", "k3", "k4"} {
		if _, ok := cache.Get(id); !ok {
			t.Errorf("expected recently used entry %s to remain cached", id)
		}
	}
}

// TestMemoryKeyCache_InvalidateKeepsListConsistent verifies that eviction and
// LRU bookkeeping stay consistent after explicit invalidation.
func TestMemoryKeyCache_InvalidateKeepsListConsistent(t *testing.T) {
	cache := NewMemoryKeyCacheWithMaxEntries(time.Minute, time.Minute, 2)
	defer cache.Close()

	cache.Set("k1", &KeyRecord{KeyID: "k1"}, 0)
	cache.Set("k2", &KeyRecord{KeyID: "k2"}, 0)
	cache.Invalidate("k1")
	cache.Set("k3", &KeyRecord{KeyID: "k3"}, 0)

	if got := len(cache.entries); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}
	if got := cache.lru.Len(); got != 2 {
		t.Fatalf("lru length = %d, want 2", got)
	}
	if _, ok := cache.Get("k3"); !ok {
		t.Fatal("expected k3 to remain cached")
	}
}
