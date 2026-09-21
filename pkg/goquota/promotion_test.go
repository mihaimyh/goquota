package goquota_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// promotionClock is a deterministic clock for promotion tests.
type promotionClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *promotionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *promotionClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// promotionClockStorage is memory storage whose TimeSource is controllable.
// Embedding promotes the memory adapter's Storage and PromotionStore methods.
type promotionClockStorage struct {
	*memory.Storage
	clock *promotionClock
}

func (s *promotionClockStorage) Now(context.Context) (time.Time, error) {
	return s.clock.Now(), nil
}

func newPromotionManager(t *testing.T) (*goquota.Manager, *promotionClockStorage) {
	t.Helper()

	clock := &promotionClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	storage := &promotionClockStorage{Storage: memory.New(), clock: clock}

	config := goquota.Config{
		DefaultTier: "free",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
			},
			"pro": {
				Name:          "pro",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
			"premium": {
				Name:          "premium",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)
	return manager, storage
}

func seedBaseEntitlement(t *testing.T, storage *promotionClockStorage, userID, tier string) *goquota.Entitlement {
	t.Helper()
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, storage.SetEntitlement(context.Background(), ent))
	return ent
}

func TestGrantPromotion_EffectiveTierAndLimits(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	tier, err := manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "free", tier)

	granted, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
		Source:    "test",
	})
	require.NoError(t, err)
	require.NotNil(t, granted.Promotion)
	assert.Equal(t, "premium", granted.Promotion.Tier)

	tier, err = manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "premium", tier)

	usage, err := manager.GetQuota(ctx, "u1", "api_calls", goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 10000, usage.Limit, "promotion tier limit must apply")

	// 200 exceeds the free limit of 100; it must succeed under premium.
	_, err = manager.Consume(ctx, "u1", "api_calls", 200, goquota.PeriodTypeMonthly)
	require.NoError(t, err)

	// Advance beyond the promotion; the base tier must resume automatically.
	storage.clock.Advance(2 * time.Hour)

	tier, err = manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "free", tier)

	usage, err = manager.GetQuota(ctx, "u1", "api_calls", goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 100, usage.Limit, "base tier limit must resume after expiry")
}

func TestGrantPromotion_DoesNotTouchBaseFields(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()

	base := seedBaseEntitlement(t, storage, "u1", "free")
	baseExpires := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	base.ExpiresAt = &baseExpires
	require.NoError(t, storage.SetEntitlement(ctx, base))

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "free", stored.Tier, "base tier must be unchanged")
	assert.True(t, stored.SubscriptionStartDate.Equal(base.SubscriptionStartDate),
		"subscription start date must be unchanged")
	require.NotNil(t, stored.ExpiresAt)
	assert.True(t, stored.ExpiresAt.Equal(baseExpires), "base expiry must be unchanged")
	assert.True(t, stored.UpdatedAt.Equal(base.UpdatedAt), "UpdatedAt must not be bumped")
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
}

func TestGrantPromotion_SurvivesProviderWrite(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	// Simulate a provider webhook/sync writing a fresh base entitlement with no
	// promotion (as the RevenueCat and Stripe adapters do).
	providerEnt := &goquota.Entitlement{
		UserID:                "u1",
		Tier:                  "pro",
		SubscriptionStartDate: storage.clock.Now(),
		UpdatedAt:             storage.clock.Now().Add(time.Minute),
	}
	require.NoError(t, manager.SetEntitlement(ctx, providerEnt))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	require.NotNil(t, stored.Promotion, "provider write must not erase the promotion")
	assert.Equal(t, "premium", stored.Promotion.Tier)

	tier, err := manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "premium", tier)

	storage.clock.Advance(2 * time.Hour)
	tier, err = manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", tier, "base provider tier resumes after expiry")
}

func TestGrantPromotion_IdempotencyKey(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	original, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:         "u1",
		Tier:           "premium",
		ExpiresAt:      storage.clock.Now().Add(time.Hour),
		IdempotencyKey: "campaign-1",
	})
	require.NoError(t, err)
	originalExpiry := original.Promotion.ExpiresAt

	storage.clock.Advance(30 * time.Minute)

	// Same idempotency key -> replay, must not extend.
	replayed, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:         "u1",
		Tier:           "premium",
		ExpiresAt:      storage.clock.Now().Add(5 * time.Hour),
		IdempotencyKey: "campaign-1",
	})
	require.NoError(t, err)
	require.NotNil(t, replayed.Promotion)
	assert.True(t, replayed.Promotion.ExpiresAt.Equal(originalExpiry),
		"idempotent replay must not extend the promotion")

	// A different key replaces the promotion.
	replaced, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:         "u1",
		Tier:           "pro",
		ExpiresAt:      storage.clock.Now().Add(2 * time.Hour),
		IdempotencyKey: "campaign-2",
	})
	require.NoError(t, err)
	assert.Equal(t, "pro", replaced.Promotion.Tier)
}

func TestGrantPromotion_Duration(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	granted, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:   "u1",
		Tier:     "premium",
		Duration: 90 * time.Minute,
	})
	require.NoError(t, err)
	want := storage.clock.Now().Add(90 * time.Minute)
	assert.True(t, granted.Promotion.ExpiresAt.Equal(want),
		"expiry = %s, want %s", granted.Promotion.ExpiresAt, want)
}

func TestGrantPromotion_AutoConsumptionAndLedgerTier(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	// GetEffectiveQuota/GetMeterQuota walk the promotion-aware consumption order.
	eff, err := manager.GetEffectiveQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "premium", eff.Tier)
	assert.Equal(t, 10000, eff.Limit)

	meter, err := manager.GetMeterQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "premium", meter.Tier)

	// PeriodTypeAuto must resolve against the promotion tier and succeed
	// beyond the free limit.
	_, err = manager.Consume(ctx, "u1", "api_calls", 500, goquota.PeriodTypeAuto)
	require.NoError(t, err)

	result, err := manager.ConsumeWithResult(ctx, "u1", "api_calls", 1, goquota.PeriodTypeAuto)
	require.NoError(t, err)
	assert.Equal(t, 10000, result.Limit)
}

func TestGrantPromotion_CreatesMissingEntitlement(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "brand-new",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	stored, err := storage.GetEntitlement(ctx, "brand-new")
	require.NoError(t, err)
	assert.Equal(t, "free", stored.Tier, "should default to the configured default tier")
	require.NotNil(t, stored.Promotion)

	storage.clock.Advance(2 * time.Hour)
	tier, err := manager.GetEffectiveTier(ctx, "brand-new")
	require.NoError(t, err)
	assert.Equal(t, "free", tier)
}

func TestGrantPromotion_Validation(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()

	future := storage.clock.Now().Add(time.Hour)
	now := storage.clock.Now()

	tests := []struct {
		name string
		req  *goquota.PromotionRequest
	}{
		{"nil request", nil},
		{"missing user", &goquota.PromotionRequest{Tier: "premium", ExpiresAt: future}},
		{"missing tier", &goquota.PromotionRequest{UserID: "u1", ExpiresAt: future}},
		{"unknown tier", &goquota.PromotionRequest{UserID: "u1", Tier: "gold", ExpiresAt: future}},
		{"no expiry", &goquota.PromotionRequest{UserID: "u1", Tier: "premium"}},
		{"past expiry", &goquota.PromotionRequest{UserID: "u1", Tier: "premium", ExpiresAt: now.Add(-time.Minute)}},
		{"negative duration", &goquota.PromotionRequest{UserID: "u1", Tier: "premium", Duration: -time.Minute}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manager.GrantPromotion(ctx, tc.req)
			assert.ErrorIs(t, err, goquota.ErrInvalidPromotionRequest)
		})
	}
}

func TestRevokePromotion(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	require.NoError(t, manager.RevokePromotion(ctx, "u1"))

	tier, err := manager.GetEffectiveTier(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "free", tier)

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Nil(t, stored.Promotion)

	// Idempotent: revoking again is not an error.
	require.NoError(t, manager.RevokePromotion(ctx, "u1"))

	// Revoking an unknown user is not an error.
	require.NoError(t, manager.RevokePromotion(ctx, "ghost"))

	// Empty user is rejected.
	assert.ErrorIs(t, manager.RevokePromotion(ctx, ""), goquota.ErrInvalidPromotionRequest)
}

func TestGetEffectiveTier_NoEntitlement(t *testing.T) {
	manager, _ := newPromotionManager(t)

	tier, err := manager.GetEffectiveTier(context.Background(), "nobody")
	require.NoError(t, err)
	assert.Equal(t, "free", tier)
}

// noPromotionStorage exposes only goquota.Storage, not PromotionStore.
type noPromotionStorage struct {
	goquota.Storage
}

func TestGrantPromotion_UnsupportedStorage(t *testing.T) {
	storage := &noPromotionStorage{Storage: memory.New()}
	config := goquota.Config{
		DefaultTier: "free",
		Tiers:       map[string]goquota.TierConfig{"free": {Name: "free"}},
	}
	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	_, err = manager.GrantPromotion(context.Background(), &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "free",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	assert.ErrorIs(t, err, goquota.ErrUnsupportedOperation)

	err = manager.RevokePromotion(context.Background(), "u1")
	assert.ErrorIs(t, err, goquota.ErrUnsupportedOperation)
}

// recordingAuditStorage records audit entries emitted by the manager.
type recordingAuditStorage struct {
	*memory.Storage
	mu      sync.Mutex
	entries []*goquota.AuditLogEntry
}

func (s *recordingAuditStorage) LogAuditEntry(_ context.Context, entry *goquota.AuditLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
	return nil
}

func (s *recordingAuditStorage) GetAuditLogs(
	_ context.Context, _ goquota.AuditLogFilter,
) ([]*goquota.AuditLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*goquota.AuditLogEntry(nil), s.entries...), nil
}

func TestGrantPromotion_WritesAuditEntry(t *testing.T) {
	storage := &recordingAuditStorage{Storage: memory.New()}
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free":    {Name: "free"},
			"premium": {Name: "premium"},
		},
	}
	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, storage.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "u1", Tier: "free", SubscriptionStartDate: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))

	_, err = manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID: "u1", Tier: "premium", ExpiresAt: time.Now().Add(time.Hour), Source: "support",
	})
	require.NoError(t, err)
	require.NoError(t, manager.RevokePromotion(ctx, "u1"))

	storage.mu.Lock()
	defer storage.mu.Unlock()
	require.Len(t, storage.entries, 2)
	assert.Equal(t, "grant_promotion", storage.entries[0].Action)
	assert.Equal(t, "premium", storage.entries[0].Metadata["tier"])
	assert.Equal(t, "support", storage.entries[0].Metadata["source"])
	assert.Equal(t, "revoke_promotion", storage.entries[1].Action)
}
