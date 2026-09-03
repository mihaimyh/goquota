package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

type fixedNowStorage struct {
	*memory.Storage
	now time.Time
}

func (s *fixedNowStorage) Now(_ context.Context) (time.Time, error) {
	return s.now, nil
}

func TestManager_GetQuota_DailyUsesEntitlementTimezone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	fixedNow := time.Date(2026, 9, 3, 1, 43, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     fixedNow.UTC(),
	}
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": 3,
				},
				ConsumptionOrder: []goquota.PeriodType{goquota.PeriodTypeDaily},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "tz-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: fixedNow.UTC(),
		UpdatedAt:             fixedNow.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	usage, err := manager.GetQuota(ctx, userID, "receipt_scan", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}

	wantEnd := time.Date(2026, 9, 4, 0, 0, 0, 0, loc).UTC()
	if !usage.Period.End.Equal(wantEnd) {
		t.Fatalf("period end = %v, want %v", usage.Period.End, wantEnd)
	}
}

func TestManager_UpdateTimezone_InvalidTimezone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {Name: "free"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	err = manager.UpdateTimezone(ctx, "user1", "Invalid/Zone")
	if err != goquota.ErrInvalidTimezone {
		t.Fatalf("err = %v, want ErrInvalidTimezone", err)
	}
}

func TestManager_UpdateTimezone_PersistsOnEntitlement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := memory.New()
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {Name: "free"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "user-tz"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	if err := manager.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone: %v", err)
	}

	ent, err := manager.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("GetEntitlement: %v", err)
	}
	if ent.Timezone != "Europe/Bucharest" {
		t.Fatalf("timezone = %q, want Europe/Bucharest", ent.Timezone)
	}

	if err := manager.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone idempotent: %v", err)
	}
}

// Reproduces the Cloud Run production failure:
// instance B caches free → instance A (RevenueCat webhook) writes premium to shared
// storage → instance B GetUserQuota syncDeviceTimezone → UpdateTimezone RMW from
// stale cache overwrites premium back to free.
func TestManager_UpdateTimezone_DoesNotClobberPremiumFromStaleCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	shared := memory.New()
	cfg := cachedTwoTierConfig()

	webhookInstance, err := goquota.NewManager(shared, cfg)
	if err != nil {
		t.Fatalf("webhook NewManager: %v", err)
	}
	quotaInstance, err := goquota.NewManager(shared, cfg)
	if err != nil {
		t.Fatalf("quota NewManager: %v", err)
	}

	userID := "user-tz-race"
	now := time.Now().UTC()
	oldExpires := now.Add(-4 * time.Hour)

	if err := quotaInstance.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		ExpiresAt:             &oldExpires,
		SubscriptionStartDate: now.Add(-24 * time.Hour),
		UpdatedAt:             now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed free entitlement: %v", err)
	}

	// Warm quota-instance cache the way GetUserQuota / GetEntitlement does in prod.
	if _, err := quotaInstance.GetEntitlement(ctx, userID); err != nil {
		t.Fatalf("warm free cache: %v", err)
	}

	premiumExpires := now.Add(30 * time.Minute)
	if err := webhookInstance.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "premium",
		ExpiresAt:             &premiumExpires,
		SubscriptionStartDate: now,
		UpdatedAt:             now,
	}); err != nil {
		t.Fatalf("webhook premium upgrade: %v", err)
	}

	storedAfterWebhook, err := shared.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("storage after webhook: %v", err)
	}
	if storedAfterWebhook.Tier != "premium" {
		t.Fatalf("storage tier after webhook = %q, want premium", storedAfterWebhook.Tier)
	}

	// Quota instance still holds stale free entitlement in its local cache.
	if err := quotaInstance.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone: %v", err)
	}

	stored, err := shared.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("storage after UpdateTimezone: %v", err)
	}
	if stored.Tier != "premium" {
		t.Fatalf("UpdateTimezone clobbered entitlement tier: got %q want premium (expires=%v timezone=%q)",
			stored.Tier, stored.ExpiresAt, stored.Timezone)
	}
	if stored.Timezone != "Europe/Bucharest" {
		t.Fatalf("timezone = %q, want Europe/Bucharest", stored.Timezone)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(premiumExpires) {
		t.Fatalf("expiresAt = %v, want %v", stored.ExpiresAt, premiumExpires)
	}
}

// Same hazard via shared storage write that does not invalidate this process cache
// (another Cloud Run revision wrote premium directly to Firestore).
func TestManager_UpdateTimezone_DoesNotClobberPremiumAfterExternalStorageWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	shared := memory.New()
	manager, err := goquota.NewManager(shared, cachedTwoTierConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "user-tz-external"
	now := time.Now().UTC()
	oldExpires := now.Add(-4 * time.Hour)

	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		ExpiresAt:             &oldExpires,
		SubscriptionStartDate: now.Add(-24 * time.Hour),
		UpdatedAt:             now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed free: %v", err)
	}
	if _, err := manager.GetEntitlement(ctx, userID); err != nil {
		t.Fatalf("warm free cache: %v", err)
	}

	premiumExpires := now.Add(30 * time.Minute)
	if err := shared.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "premium",
		ExpiresAt:             &premiumExpires,
		SubscriptionStartDate: now,
		UpdatedAt:             now,
	}); err != nil {
		t.Fatalf("external premium write: %v", err)
	}

	if err := manager.UpdateTimezone(ctx, userID, "Europe/Bucharest"); err != nil {
		t.Fatalf("UpdateTimezone: %v", err)
	}

	stored, err := shared.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("storage after UpdateTimezone: %v", err)
	}
	if stored.Tier != "premium" {
		t.Fatalf("UpdateTimezone clobbered entitlement tier: got %q want premium (expires=%v timezone=%q)",
			stored.Tier, stored.ExpiresAt, stored.Timezone)
	}
	if stored.Timezone != "Europe/Bucharest" {
		t.Fatalf("timezone = %q, want Europe/Bucharest", stored.Timezone)
	}
}

func cachedTwoTierConfig() *goquota.Config {
	return &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free":    {Name: "free"},
			"premium": {Name: "premium"},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  5 * time.Minute,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}
}

func cachedDailyScanConfig() *goquota.Config {
	return &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": 1,
					"receipt_chat": 5,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeDaily,
					goquota.PeriodTypeForever,
				},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  5 * time.Minute,
			UsageTTL:        5 * time.Second,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}
}

// Reproduces production: GetUserQuota caches the entitlement, then Consume /
// GetQuota on the same instance strips Timezone and keeps the UTC day.
func TestManager_GetQuota_CacheHitKeepsLocalMidnight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// 00:30 local Sept 4 == 21:30 UTC Sept 3 (still Sept 3 in UTC).
	afterMidnight := time.Date(2026, 9, 4, 0, 30, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     afterMidnight.UTC(),
	}
	manager, err := goquota.NewManager(storage, cachedDailyScanConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "tz-cache-hit-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: afterMidnight.UTC(),
		UpdatedAt:             afterMidnight.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	warmed, err := manager.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("warm GetEntitlement: %v", err)
	}
	if warmed.Timezone != "Europe/Bucharest" {
		t.Fatalf("storage/miss timezone = %q, want Europe/Bucharest", warmed.Timezone)
	}

	cached, err := manager.GetEntitlement(ctx, userID)
	if err != nil {
		t.Fatalf("cached GetEntitlement: %v", err)
	}
	if cached.Timezone != "Europe/Bucharest" {
		t.Fatalf("cache-hit timezone = %q, want Europe/Bucharest", cached.Timezone)
	}

	usage, err := manager.GetQuota(ctx, userID, "receipt_chat", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota after cache hit: %v", err)
	}

	wantEnd := time.Date(2026, 9, 5, 0, 0, 0, 0, loc).UTC()
	if !usage.Period.End.Equal(wantEnd) {
		t.Fatalf("cache-hit period end = %v, want local midnight %v (UTC fallback keeps Sept 3)", usage.Period.End, wantEnd)
	}
	if usage.Period.Key() != "2026-09-04" {
		t.Fatalf("cache-hit period key = %q, want 2026-09-04", usage.Period.Key())
	}
}

func TestManager_Consume_CacheHitResetsAtLocalMidnight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	beforeMidnight := time.Date(2026, 9, 3, 23, 30, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     beforeMidnight.UTC(),
	}
	manager, err := goquota.NewManager(storage, cachedDailyScanConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "consume-tz-cache-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: beforeMidnight.UTC(),
		UpdatedAt:             beforeMidnight.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	if _, err := manager.Consume(ctx, userID, "receipt_chat", 5, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume before midnight: %v", err)
	}

	if _, err := manager.GetEntitlement(ctx, userID); err != nil {
		t.Fatalf("warm entitlement cache: %v", err)
	}

	_, err = manager.Consume(ctx, userID, "receipt_chat", 1, goquota.PeriodTypeDaily)
	if err != goquota.ErrQuotaExceeded {
		t.Fatalf("expected quota exceeded before local reset, got %v", err)
	}

	afterMidnight := time.Date(2026, 9, 4, 0, 30, 0, 0, loc)
	storage.now = afterMidnight.UTC()

	if _, err := manager.Consume(ctx, userID, "receipt_chat", 1, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume after local midnight with warm cache: %v", err)
	}

	usage, err := manager.GetQuota(ctx, userID, "receipt_chat", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota after reset: %v", err)
	}
	if usage.Used != 1 {
		t.Fatalf("used after local reset = %d, want 1", usage.Used)
	}
	if usage.Period.Key() != "2026-09-04" {
		t.Fatalf("period key after local reset = %q, want 2026-09-04", usage.Period.Key())
	}
}

func TestManager_Consume_ResetsAtUserLocalMidnight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	beforeMidnight := time.Date(2026, 9, 3, 23, 30, 0, 0, loc)
	storage := &fixedNowStorage{
		Storage: memory.New(),
		now:     beforeMidnight.UTC(),
	}
	manager, err := goquota.NewManager(storage, &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				DailyQuotas: map[string]int{
					"receipt_scan": 1,
				},
				ConsumptionOrder: []goquota.PeriodType{goquota.PeriodTypeDaily},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	userID := "consume-tz-user"
	if err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		Timezone:              "Europe/Bucharest",
		SubscriptionStartDate: beforeMidnight.UTC(),
		UpdatedAt:             beforeMidnight.UTC(),
	}); err != nil {
		t.Fatalf("SetEntitlement: %v", err)
	}

	if _, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	_, err = manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily)
	if err != goquota.ErrQuotaExceeded {
		t.Fatalf("expected quota exceeded before local reset, got %v", err)
	}

	afterMidnight := time.Date(2026, 9, 4, 0, 30, 0, 0, loc)
	storage.now = afterMidnight.UTC()

	if _, err := manager.Consume(ctx, userID, "receipt_scan", 1, goquota.PeriodTypeDaily); err != nil {
		t.Fatalf("Consume after local midnight: %v", err)
	}
}
