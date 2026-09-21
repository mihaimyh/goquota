package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func baseEntitlement(userID, tier string) *goquota.Entitlement {
	return &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestStorage_SetPromotion_RoundTrip(t *testing.T) {
	storage := New()
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("u1", "free")))

	promo := &goquota.TierPromotion{
		Tier:      "premium",
		GrantedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Source:    "test",
	}
	require.NoError(t, storage.SetPromotion(ctx, "u1", promo))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
	assert.Equal(t, "test", stored.Promotion.Source)
	assert.Equal(t, "free", stored.Tier, "base tier must be unchanged")

	// Mutating the caller's struct must not affect the stored copy.
	promo.Tier = "mutated"
	stored, err = storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "premium", stored.Promotion.Tier)
}

func TestStorage_SetPromotion_MissingEntitlement(t *testing.T) {
	storage := New()
	err := storage.SetPromotion(context.Background(), "ghost", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	})
	assert.ErrorIs(t, err, goquota.ErrEntitlementNotFound)
}

func TestStorage_ClearPromotion_Idempotent(t *testing.T) {
	storage := New()
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("u1", "free")))
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	}))

	require.NoError(t, storage.ClearPromotion(ctx, "u1"))
	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Nil(t, stored.Promotion)

	// Idempotent for both existing and missing users.
	require.NoError(t, storage.ClearPromotion(ctx, "u1"))
	require.NoError(t, storage.ClearPromotion(ctx, "ghost"))
}

func TestStorage_SetEntitlement_PreservesPromotion(t *testing.T) {
	storage := New()
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("u1", "free")))
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	}))

	// A provider-style write with no promotion must preserve the overlay.
	providerEnt := baseEntitlement("u1", "pro")
	providerEnt.UpdatedAt = time.Now().UTC().Add(time.Minute)
	require.NoError(t, storage.SetEntitlement(ctx, providerEnt))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)

	// An explicit promotion write replaces the overlay.
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "pro", ExpiresAt: time.Now().Add(2 * time.Hour),
	}))
	stored, err = storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Promotion.Tier)
}

func TestStorage_SetPromotion_RejectsInvalid(t *testing.T) {
	storage := New()
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("u1", "free")))

	assert.Error(t, storage.SetPromotion(ctx, "u1", nil))
	assert.Error(t, storage.SetPromotion(ctx, "", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	}))
	assert.Error(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{Tier: "premium"}))
}
