package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func newPromotionTestStorage(t *testing.T, name string) *Storage {
	t.Helper()
	client := setupFirestoreClient(t)
	t.Cleanup(func() { _ = client.Close() })

	entColl, usageColl := getTestCollections(name)
	t.Cleanup(func() { cleanupFirestore(t, client, entColl, usageColl) })

	storage, err := New(client, Config{
		EntitlementsCollection: entColl,
		UsageCollection:        usageColl,
	})
	require.NoError(t, err)
	return storage
}

func firestoreBaseEntitlement(userID, tier string) *goquota.Entitlement {
	return &goquota.Entitlement{
		UserID:                userID,
		Tier:                  tier,
		SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestFirestore_PromotionRoundTrip(t *testing.T) {
	storage := newPromotionTestStorage(t, "promo_round_trip")
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, firestoreBaseEntitlement("u1", "free")))

	expires := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier:           "premium",
		GrantedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:      expires,
		Source:         "test",
		Reason:         "support_comp",
		IdempotencyKey: "key-1",
	}))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
	assert.Equal(t, "test", stored.Promotion.Source)
	assert.Equal(t, "support_comp", stored.Promotion.Reason)
	assert.Equal(t, "key-1", stored.Promotion.IdempotencyKey)
	assert.True(t, stored.Promotion.ExpiresAt.Equal(expires))
	assert.Equal(t, "free", stored.Tier)

	require.NoError(t, storage.ClearPromotion(ctx, "u1"))
	stored, err = storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Nil(t, stored.Promotion)
}

func TestFirestore_SetEntitlement_PreservesPromotion(t *testing.T) {
	storage := newPromotionTestStorage(t, "promo_preserve")
	ctx := context.Background()
	require.NoError(t, storage.SetEntitlement(ctx, firestoreBaseEntitlement("u1", "free")))
	require.NoError(t, storage.SetPromotion(ctx, "u1", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	// Provider-style write with no promotion.
	require.NoError(t, storage.SetEntitlement(ctx, firestoreBaseEntitlement("u1", "pro")))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	require.NotNil(t, stored.Promotion)
	assert.Equal(t, "premium", stored.Promotion.Tier)
}

func TestFirestore_PromotionErrors(t *testing.T) {
	storage := newPromotionTestStorage(t, "promo_errors")
	ctx := context.Background()

	assert.ErrorIs(t, storage.SetPromotion(ctx, "ghost", &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: time.Now().Add(time.Hour),
	}), goquota.ErrEntitlementNotFound)

	// Clear is idempotent for missing users.
	require.NoError(t, storage.ClearPromotion(ctx, "ghost"))
}
