package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestResolveEffectiveTier pins the canonical resolver that every consumer of a
// user's tier must go through. Reading Entitlement.Tier directly is the class of
// bug the resolver exists to prevent: it is the base tier, not the tier in
// effect while a promotion is active.
func TestResolveEffectiveTier(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	active := &goquota.TierPromotion{Tier: "premium", ExpiresAt: now.Add(time.Hour)}
	expired := &goquota.TierPromotion{Tier: "premium", ExpiresAt: now.Add(-time.Minute)}
	atBoundary := &goquota.TierPromotion{Tier: "premium", ExpiresAt: now}

	tests := []struct {
		name string
		ent  *goquota.Entitlement
		def  string
		want string
	}{
		{"nil entitlement uses default", nil, "free", "free"},
		{"empty base tier uses default", &goquota.Entitlement{}, "free", "free"},
		{"base tier wins when no promotion", &goquota.Entitlement{Tier: "pro"}, "free", "pro"},
		{
			"active promotion overrides base",
			&goquota.Entitlement{Tier: "pro", Promotion: active},
			"free", "premium",
		},
		{
			"expired promotion is ignored",
			&goquota.Entitlement{Tier: "pro", Promotion: expired},
			"free", "pro",
		},
		{
			"promotion is inactive exactly at expiry",
			&goquota.Entitlement{Tier: "pro", Promotion: atBoundary},
			"free", "pro",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, goquota.ResolveEffectiveTier(tc.ent, tc.def, now))
			// The method form is the ergonomic entry point for callers that hold
			// only an entitlement (e.g. an app-side DTO mapping layer).
			assert.Equal(t, tc.want, tc.ent.EffectiveTier(tc.def, now))
		})
	}
}

func TestEntitlementActivePromotion(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	promo := &goquota.TierPromotion{Tier: "premium", ExpiresAt: now.Add(time.Hour)}

	// Nil-safe: an absent entitlement has no active promotion.
	var nilEnt *goquota.Entitlement
	assert.Nil(t, nilEnt.ActivePromotion(now))
	assert.Nil(t, (&goquota.Entitlement{}).ActivePromotion(now))

	assert.Same(t, promo, (&goquota.Entitlement{Promotion: promo}).ActivePromotion(now))

	expired := &goquota.TierPromotion{Tier: "premium", ExpiresAt: now.Add(-time.Second)}
	assert.Nil(t, (&goquota.Entitlement{Promotion: expired}).ActivePromotion(now))
	assert.Nil(t, (&goquota.Entitlement{Promotion: &goquota.TierPromotion{Tier: "premium", ExpiresAt: now}}).
		ActivePromotion(now), "expiry boundary is not active")
}

// TestGetQuotaExposesActivePromotion covers both the cache-miss and cache-hit
// paths: a caller that only reads GetQuota must see the promotion (and the
// effective tier) without a second call or an app-side resolver.
func TestGetQuotaExposesActivePromotion(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
		Source:    "test",
	})
	require.NoError(t, err)

	for attempt := 0; attempt < 2; attempt++ { // second call exercises the usage cache
		usage, err := manager.GetQuota(ctx, "u1", "api_calls", goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, "premium", usage.Tier, "Tier is the effective tier")
		require.NotNil(t, usage.Promotion, "attempt %d must expose the active promotion", attempt)
		assert.Equal(t, "premium", usage.Promotion.Tier)
	}

	// The promotion expires lazily: no invalidation is required, the read-time
	// resolution must drop the overlay and fall back to the base tier.
	storage.clock.Advance(2 * time.Hour)

	usage, err := manager.GetQuota(ctx, "u1", "api_calls", goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, "free", usage.Tier)
	assert.Nil(t, usage.Promotion, "an expired promotion must not be reported")
}

func TestGetEffectiveQuotaAndMeterQuotaExposePromotion(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	eff, err := manager.GetEffectiveQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "premium", eff.Tier)
	require.NotNil(t, eff.Promotion)

	meter, err := manager.GetMeterQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "premium", meter.Tier)
	require.NotNil(t, meter.Promotion)

	storage.clock.Advance(2 * time.Hour)

	eff, err = manager.GetEffectiveQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "free", eff.Tier)
	assert.Nil(t, eff.Promotion)

	meter, err = manager.GetMeterQuota(ctx, "u1", "api_calls")
	require.NoError(t, err)
	assert.Equal(t, "free", meter.Tier)
	assert.Nil(t, meter.Promotion)
}

func TestConsumeWithResultExposesEffectiveTier(t *testing.T) {
	manager, storage := newPromotionManager(t)
	ctx := context.Background()
	seedBaseEntitlement(t, storage, "u1", "free")

	_, err := manager.GrantPromotion(ctx, &goquota.PromotionRequest{
		UserID:    "u1",
		Tier:      "premium",
		ExpiresAt: storage.clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	result, err := manager.ConsumeWithResult(ctx, "u1", "api_calls", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, "premium", result.EffectiveTier, "consume side-effects need the effective tier")
	require.NotNil(t, result.Promotion)

	// Zero-amount auto consumption resolves through GetEffectiveQuota.
	zero, err := manager.ConsumeWithResult(ctx, "u1", "api_calls", 0, goquota.PeriodTypeAuto)
	require.NoError(t, err)
	assert.Equal(t, "premium", zero.EffectiveTier)
	require.NotNil(t, zero.Promotion)

	storage.clock.Advance(2 * time.Hour)

	result, err = manager.ConsumeWithResult(ctx, "u1", "api_calls", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, "free", result.EffectiveTier)
	assert.Nil(t, result.Promotion)
}
