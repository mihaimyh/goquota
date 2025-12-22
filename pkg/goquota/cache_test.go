package goquota_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

const (
	testUserID1  = "user1"
	testTierFree = "free"
)

func TestLRUCache_Entitlement(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	// Test cache miss
	_, found := cache.GetEntitlement(testUserID1)
	if found {
		t.Error("Expected cache miss for non-existent entitlement")
	}

	// Test cache set and get
	ent := &goquota.Entitlement{
		UserID:                testUserID1,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	cache.SetEntitlement(testUserID1, ent, time.Minute)

	cached, found := cache.GetEntitlement(testUserID1)
	if !found {
		t.Fatal("Expected cache hit")
	}
	if cached.UserID != testUserID1 || cached.Tier != "pro" {
		t.Errorf("Cached entitlement mismatch: got %+v", cached)
	}

	// Test cache invalidation
	cache.InvalidateEntitlement(testUserID1)
	_, found = cache.GetEntitlement(testUserID1)
	if found {
		t.Error("Expected cache miss after invalidation")
	}
}

func TestLRUCache_Usage(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	// Test cache miss
	_, found := cache.GetUsage("key1")
	if found {
		t.Error("Expected cache miss for non-existent usage")
	}

	// Test cache set and get
	usage := &goquota.Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
		Tier:     "pro",
	}
	cache.SetUsage("key1", usage, time.Minute)

	cached, found := cache.GetUsage("key1")
	if !found {
		t.Fatal("Expected cache hit")
	}
	if cached.Used != 50 || cached.Limit != 100 {
		t.Errorf("Cached usage mismatch: got %+v", cached)
	}

	// Test cache invalidation
	cache.InvalidateUsage("key1")
	_, found = cache.GetUsage("key1")
	if found {
		t.Error("Expected cache miss after invalidation")
	}
}

func TestLRUCache_TTL(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	ent := &goquota.Entitlement{
		UserID: "user1",
		Tier:   "pro",
	}

	// Set with very short TTL
	cache.SetEntitlement("user1", ent, 10*time.Millisecond)

	// Should be cached immediately
	_, found := cache.GetEntitlement("user1")
	if !found {
		t.Error("Expected cache hit immediately after set")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should be expired
	_, found = cache.GetEntitlement("user1")
	if found {
		t.Error("Expected cache miss after TTL expiration")
	}
}

func TestLRUCache_Stats(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	// Initial stats
	stats := cache.Stats()
	if stats.EntitlementHits != 0 || stats.EntitlementMisses != 0 {
		t.Error("Expected zero stats initially")
	}

	// Cause a miss
	cache.GetEntitlement("user1")
	stats = cache.Stats()
	if stats.EntitlementMisses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.EntitlementMisses)
	}

	// Set and cause a hit
	ent := &goquota.Entitlement{UserID: "user1", Tier: "pro"}
	cache.SetEntitlement("user1", ent, time.Minute)
	cache.GetEntitlement("user1")

	stats = cache.Stats()
	if stats.EntitlementHits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.EntitlementHits)
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	// Small cache that can only hold 2 entitlements
	cache := goquota.NewLRUCache(2, 2)

	// Add 3 entitlements (should evict one)
	for i := 1; i <= 3; i++ {
		ent := &goquota.Entitlement{
			UserID: string(rune('0' + i)),
			Tier:   "pro",
		}
		cache.SetEntitlement(ent.UserID, ent, time.Minute)
	}

	stats := cache.Stats()
	if stats.Evictions == 0 {
		t.Error("Expected at least one eviction")
	}
}

func TestLRUCache_Clear(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	// Add some data
	ent := &goquota.Entitlement{UserID: "user1", Tier: "pro"}
	cache.SetEntitlement("user1", ent, time.Minute)

	usage := &goquota.Usage{UserID: "user1", Resource: "api", Used: 10}
	cache.SetUsage("key1", usage, time.Minute)

	// Clear cache
	cache.Clear()

	// Verify everything is gone
	_, found := cache.GetEntitlement("user1")
	if found {
		t.Error("Expected cache miss after clear")
	}

	_, found = cache.GetUsage("key1")
	if found {
		t.Error("Expected cache miss after clear")
	}
}

// Phase 9: Cache Operations - Noop Cache Tests

func TestNoopCache_SetEntitlement(t *testing.T) {
	cache := goquota.NewNoopCache()

	ent := &goquota.Entitlement{UserID: "user1", Tier: "pro"}
	cache.SetEntitlement("user1", ent, time.Minute)

	// Should be no-op - verify it doesn't store
	_, found := cache.GetEntitlement("user1")
	if found {
		t.Error("NoopCache should always return cache miss")
	}
}

func TestNoopCache_InvalidateEntitlement(t *testing.T) {
	cache := goquota.NewNoopCache()

	// Invalidate should be no-op (no error)
	cache.InvalidateEntitlement("user1")

	// Verify it's still a no-op
	_, found := cache.GetEntitlement("user1")
	if found {
		t.Error("NoopCache should always return cache miss")
	}
}

func TestNoopCache_GetUsage(t *testing.T) {
	cache := goquota.NewNoopCache()

	// GetUsage should always return false
	_, found := cache.GetUsage("key1")
	if found {
		t.Error("NoopCache GetUsage should always return false")
	}
}

func TestNoopCache_SetUsage(t *testing.T) {
	cache := goquota.NewNoopCache()

	usage := &goquota.Usage{
		UserID:   "user1",
		Resource: "api_calls",
		Used:     50,
		Limit:    100,
	}
	cache.SetUsage("key1", usage, time.Minute)

	// Should be no-op - verify it doesn't store
	_, found := cache.GetUsage("key1")
	if found {
		t.Error("NoopCache should always return cache miss")
	}
}

func TestNoopCache_InvalidateUsage(t *testing.T) {
	cache := goquota.NewNoopCache()

	// Invalidate should be no-op (no error)
	cache.InvalidateUsage("key1")

	// Verify it's still a no-op
	_, found := cache.GetUsage("key1")
	if found {
		t.Error("NoopCache should always return cache miss")
	}
}

func TestNoopCache_Clear(t *testing.T) {
	cache := goquota.NewNoopCache()

	// Clear should be no-op (no error)
	cache.Clear()

	// Verify stats are still zero
	stats := cache.Stats()
	if stats.EntitlementHits != 0 || stats.UsageHits != 0 || stats.Size != 0 {
		t.Error("NoopCache stats should always be zero")
	}
}

func TestNoopCache_Stats(t *testing.T) {
	cache := goquota.NewNoopCache()

	// Perform various operations
	ent := &goquota.Entitlement{UserID: "user1", Tier: "pro"}
	cache.SetEntitlement("user1", ent, time.Minute)
	cache.GetEntitlement("user1")
	cache.InvalidateEntitlement("user1")

	usage := &goquota.Usage{UserID: "user1", Resource: "api", Used: 10}
	cache.SetUsage("key1", usage, time.Minute)
	cache.GetUsage("key1")
	cache.InvalidateUsage("key1")
	cache.Clear()

	// Stats should always return zero values
	stats := cache.Stats()
	if stats.EntitlementHits != 0 {
		t.Errorf("Expected EntitlementHits 0, got %d", stats.EntitlementHits)
	}
	if stats.EntitlementMisses != 0 {
		t.Errorf("Expected EntitlementMisses 0, got %d", stats.EntitlementMisses)
	}
	if stats.UsageHits != 0 {
		t.Errorf("Expected UsageHits 0, got %d", stats.UsageHits)
	}
	if stats.UsageMisses != 0 {
		t.Errorf("Expected UsageMisses 0, got %d", stats.UsageMisses)
	}
	if stats.Evictions != 0 {
		t.Errorf("Expected Evictions 0, got %d", stats.Evictions)
	}
	if stats.Size != 0 {
		t.Errorf("Expected Size 0, got %d", stats.Size)
	}
}

func TestManager_WithCache(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api": 100},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  time.Minute,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()

	// Set entitlement
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// First GetEntitlement should cache it
	retrieved, err := manager.GetEntitlement(ctx, testUserID1)
	if err != nil {
		t.Fatalf("GetEntitlement failed: %v", err)
	}
	if retrieved.Tier != testTierFree {
		t.Errorf("Expected tier 'free', got '%s'", retrieved.Tier)
	}

	// Second call should hit cache (we can't directly verify this without exposing cache,
	// but we can verify it works correctly)
	retrieved2, err := manager.GetEntitlement(ctx, "user1")
	if err != nil {
		t.Fatalf("GetEntitlement (cached) failed: %v", err)
	}
	if retrieved2.Tier != "free" {
		t.Errorf("Expected tier 'free', got '%s'", retrieved2.Tier)
	}
}

func BenchmarkCache_GetEntitlement(b *testing.B) {
	cache := goquota.NewLRUCache(1000, 1000)
	ent := &goquota.Entitlement{
		UserID: "user1",
		Tier:   "pro",
	}
	cache.SetEntitlement("user1", ent, time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.GetEntitlement("user1")
	}
}

func BenchmarkCache_SetEntitlement(b *testing.B) {
	cache := goquota.NewLRUCache(1000, 1000)
	ent := &goquota.Entitlement{
		UserID: "user1",
		Tier:   "pro",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.SetEntitlement("user1", ent, time.Minute)
	}
}

// Phase 7.2: Cache Consistency Tests

func TestCache_StaleCacheAfterTierChange(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
			"fluent": {
				Name:          "fluent",
				MonthlyQuotas: map[string]int{"api_calls": 5000},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  time.Minute,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()

	// Set initial entitlement
	ent := &goquota.Entitlement{
		UserID:                "user_cache_tier",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Get quota to populate cache
	usage1, err := manager.GetQuota(ctx, "user_cache_tier", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage1.Limit != 1000 {
		t.Errorf("Expected limit 1000, got %d", usage1.Limit)
	}

	// Change tier
	err = manager.ApplyTierChange(ctx, "user_cache_tier", "scholar", "fluent", "api_calls")
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Update entitlement
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_cache_tier",
		Tier:                  "fluent",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement update failed: %v", err)
	}

	// Get quota again - cache should be invalidated and show new limit
	usage2, err := manager.GetQuota(ctx, "user_cache_tier", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota after tier change failed: %v", err)
	}
	// Limit should reflect new tier (prorated)
	if usage2.Limit <= 1000 {
		t.Errorf("Expected limit > 1000 (new tier), got %d", usage2.Limit)
	}
}

func TestCache_ExpirationDuringOperation(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	ent := &goquota.Entitlement{
		UserID: "user1",
		Tier:   "pro",
	}

	// Set with very short TTL
	cache.SetEntitlement("user1", ent, 10*time.Millisecond)

	// Verify it's cached
	_, found := cache.GetEntitlement("user1")
	if !found {
		t.Error("Expected cache hit immediately")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should be expired now
	_, found = cache.GetEntitlement("user1")
	if found {
		t.Error("Expected cache miss after expiration")
	}
}

func TestCache_EvictionUnderLoad(t *testing.T) {
	// Small cache that can only hold 3 entitlements
	cache := goquota.NewLRUCache(3, 3)

	// Add 5 entitlements (should evict 2)
	for i := 1; i <= 5; i++ {
		ent := &goquota.Entitlement{
			UserID: fmt.Sprintf("user%d", i),
			Tier:   "pro",
		}
		cache.SetEntitlement(ent.UserID, ent, time.Minute)
	}

	stats := cache.Stats()
	if stats.Evictions < 2 {
		t.Errorf("Expected at least 2 evictions, got %d", stats.Evictions)
	}

	// First 2 should be evicted (LRU)
	_, found1 := cache.GetEntitlement("user1")
	_, found2 := cache.GetEntitlement("user2")
	if found1 || found2 {
		t.Error("Expected user1 and user2 to be evicted")
	}

	// Last 3 should still be cached
	for i := 3; i <= 5; i++ {
		_, found := cache.GetEntitlement(fmt.Sprintf("user%d", i))
		if !found {
			t.Errorf("Expected user%d to still be cached", i)
		}
	}
}

func TestCache_ConcurrentInvalidation(t *testing.T) {
	cache := goquota.NewLRUCache(100, 100)

	// Populate cache
	for i := 1; i <= 50; i++ {
		ent := &goquota.Entitlement{
			UserID: fmt.Sprintf("user%d", i),
			Tier:   "pro",
		}
		cache.SetEntitlement(ent.UserID, ent, time.Minute)
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)

	// Concurrent invalidations
	for i := 1; i <= goroutines; i++ {
		go func(id int) {
			cache.InvalidateEntitlement(fmt.Sprintf("user%d", id))
			errChan <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent invalidation %d failed: %v", i, err)
		}
	}

	// Verify all were invalidated
	for i := 1; i <= 50; i++ {
		_, found := cache.GetEntitlement(fmt.Sprintf("user%d", i))
		if found {
			t.Errorf("Expected user%d to be invalidated", i)
		}
	}
}
