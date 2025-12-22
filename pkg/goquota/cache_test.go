package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestLRUCache_Entitlement(t *testing.T) {
	cache := goquota.NewLRUCache(10, 10)

	// Test cache miss
	_, found := cache.GetEntitlement("user1")
	if found {
		t.Error("Expected cache miss for non-existent entitlement")
	}

	// Test cache set and get
	ent := &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	cache.SetEntitlement("user1", ent, time.Minute)

	cached, found := cache.GetEntitlement("user1")
	if !found {
		t.Fatal("Expected cache hit")
	}
	if cached.UserID != "user1" || cached.Tier != "pro" {
		t.Errorf("Cached entitlement mismatch: got %+v", cached)
	}

	// Test cache invalidation
	cache.InvalidateEntitlement("user1")
	_, found = cache.GetEntitlement("user1")
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

func TestNoopCache(t *testing.T) {
	cache := goquota.NewNoopCache()

	// All operations should be no-ops
	ent := &goquota.Entitlement{UserID: "user1", Tier: "pro"}
	cache.SetEntitlement("user1", ent, time.Minute)

	_, found := cache.GetEntitlement("user1")
	if found {
		t.Error("NoopCache should always return cache miss")
	}

	stats := cache.Stats()
	if stats.EntitlementHits != 0 || stats.Size != 0 {
		t.Error("NoopCache stats should always be zero")
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

	manager, err := goquota.NewManager(storage, config)
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
	retrieved, err := manager.GetEntitlement(ctx, "user1")
	if err != nil {
		t.Fatalf("GetEntitlement failed: %v", err)
	}
	if retrieved.Tier != "free" {
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
