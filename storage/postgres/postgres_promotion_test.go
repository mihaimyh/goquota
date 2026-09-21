//go:build integration
// +build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func pgBaseEntitlement(userID, tier string) *goquota.Entitlement {
	return &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestPostgres_PromotionRoundTrip(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	require.NoError(t, storage.SetEntitlement(ctx, pgBaseEntitlement("u1", "free")))

	expires := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier:           "premium",
		GrantedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:      expires,
		Source:         "test",
		IdempotencyKey: "key-1",
	}))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
	assert.Equal(t, "test", stored.Promotion.Source)
	assert.Equal(t, "key-1", stored.Promotion.IdempotencyKey)
	assert.True(t, stored.Promotion.ExpiresAt.Equal(expires))
	assert.Equal(t, "free", stored.Tier)

	require.NoError(t, storage.ClearPromotion(ctx, "u1"))
	stored, err = storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Nil(t, stored.Promotion)
}

func TestPostgres_SetEntitlement_PreservesPromotion(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	require.NoError(t, storage.SetEntitlement(ctx, pgBaseEntitlement("u1", "free")))
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	// Provider-style write with no promotion must preserve the overlay.
	require.NoError(t, storage.SetEntitlement(ctx, pgBaseEntitlement("u1", "pro")))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
}

func TestPostgres_PromotionErrors(t *testing.T) {
	storage := setupTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	assert.ErrorIs(t, storage.SetPromotion(ctx, "ghost", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	}), goquota.ErrEntitlementNotFound)

	require.NoError(t, storage.ClearPromotion(ctx, "ghost"))
}
