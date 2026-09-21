package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManagerInvalidateEntitlement(t *testing.T) {
	store := memory.New()
	cfg := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {Name: "free"},
			"pro":  {Name: "pro"},
		},
		CacheConfig: &goquota.CacheConfig{Enabled: true, EntitlementTTL: time.Minute},
	}
	manager, err := goquota.NewManager(store, &cfg)
	require.NoError(t, err)
	ctx := context.Background()
	now := time.Now().UTC()

	require.NoError(t, store.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "u1", Tier: "free", SubscriptionStartDate: now, UpdatedAt: now,
	}))

	// Prime the entitlement cache.
	ent, err := manager.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, "free", ent.Tier)

	// A change made out of band (another instance / another process) is not
	// visible to this instance until its cache entry is dropped.
	require.NoError(t, store.SetEntitlement(ctx, &goquota.Entitlement{
		UserID: "u1", Tier: "pro", SubscriptionStartDate: now, UpdatedAt: now,
	}))

	stale, err := manager.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, "free", stale.Tier, "cache should still serve the stale entry")

	manager.InvalidateEntitlement("u1")

	fresh, err := manager.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", fresh.Tier, "invalidation must make the next read hit storage")

	// Safe for unknown identities.
	manager.InvalidateEntitlement("ghost")
}
