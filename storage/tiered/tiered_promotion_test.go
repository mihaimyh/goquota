package tiered

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func tieredBaseEntitlement(userID, tier string) *goquota.Entitlement {
	return &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestTiered_PromotionWriteThrough(t *testing.T) {
	hot := memory.New()
	cold := memory.New()
	storage, err := New(Config{Hot: hot, Cold: cold})
	require.NoError(t, err)
	defer storage.Close()

	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, tieredBaseEntitlement("u1", "free")))
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "premium", GrantedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	// Both tiers of the cache carry the promotion.
	for name, s := range map[string]goquota.Storage{"hot": hot, "cold": cold} {
		ent, err := s.GetEntitlement(ctx, "u1")
		require.NoError(t, err, name)
		require.NotNil(t, ent.Promotion, name)
		assert.Equal(t, "premium", ent.Promotion.Tier, name)
	}

	// Provider-style base write preserves the promotion.
	require.NoError(t, storage.SetEntitlement(ctx, tieredBaseEntitlement("u1", "pro")))
	ent, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", ent.Tier)
	require.NotNil(t, ent.Promotion)

	require.NoError(t, storage.ClearPromotion(ctx, "u1"))
	for name, s := range map[string]goquota.Storage{"hot": hot, "cold": cold} {
		ent, err := s.GetEntitlement(ctx, "u1")
		require.NoError(t, err, name)
		assert.Nil(t, ent.Promotion, name)
	}
}

// storageOnly hides the optional PromotionStore capability.
type storageOnly struct {
	goquota.Storage
}

func TestTiered_PromotionUnsupported(t *testing.T) {
	storage, err := New(Config{
		Hot:  storageOnly{memory.New()},
		Cold: storageOnly{memory.New()},
	})
	require.NoError(t, err)
	defer storage.Close()

	err = storage.SetPromotion(context.Background(), "u1", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	})
	assert.ErrorIs(t, err, goquota.ErrUnsupportedOperation)

	err = storage.ClearPromotion(context.Background(), "u1")
	assert.ErrorIs(t, err, goquota.ErrUnsupportedOperation)
}
