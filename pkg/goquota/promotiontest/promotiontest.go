// Package promotiontest provides a reusable conformance suite for the
// goquota.PromotionStore contract.
//
// Storage adapters (and any wrapper that forwards PromotionStore) should run it
// from their own test suite:
//
//	func TestPromotionConformance(t *testing.T) {
//		promotiontest.Run(t, func(t *testing.T) promotiontest.Store {
//			return memory.New()
//		})
//	}
//
// The suite encodes the guarantees documented in docs/PROMOTIONS.md, most
// importantly that a promotion is an overlay: promotion writes never modify the
// provider-owned base fields (Tier, SubscriptionStartDate, UpdatedAt), and a
// provider write with a nil Promotion never erases an existing one. Centralizing
// these checks means the contract is only as strong as one implementation, not
// as the weakest hand-written copy per adapter.
package promotiontest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Store is a promotion-capable storage backend.
type Store interface {
	goquota.Storage
	goquota.PromotionStore
}

// Factory returns a fresh, empty, isolated Store. It is called once per Run;
// subtests are isolated by user ID, so the store may be shared.
type Factory func(t *testing.T) Store

const userPrefix = "promotiontest"

// baseTime is a fixed, non-zero timestamp used for all seeded base fields.
var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Run executes the promotion conformance suite against newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	store := newStore(t)

	t.Run("RoundTrip", func(t *testing.T) { testRoundTrip(t, store) })
	t.Run("ReplacesExistingPromotion", func(t *testing.T) { testReplacesExisting(t, store) })
	t.Run("MissingEntitlement", func(t *testing.T) { testMissingEntitlement(t, store) })
	t.Run("ClearIsIdempotent", func(t *testing.T) { testClearIsIdempotent(t, store) })
	t.Run("BaseWritePreservesPromotion", func(t *testing.T) { testBaseWritePreserves(t, store) })
	t.Run("ClearPreservesBaseFields", func(t *testing.T) { testClearPreservesBaseFields(t, store) })
	t.Run("WritesLeaveUpdatedAtUntouched", func(t *testing.T) { testWritesLeaveUpdatedAtUntouched(t, store) })
}

// seed creates a base entitlement with fixed base fields.
func seed(t *testing.T, store Store, userID string) {
	t.Helper()
	ent := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: baseTime,
		UpdatedAt:             baseTime,
	}
	require.NoError(t, store.SetEntitlement(context.Background(), ent))
}

func mustGet(t *testing.T, store Store, userID string) *goquota.Entitlement {
	t.Helper()
	ent, err := store.GetEntitlement(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, ent)
	return ent
}

func testRoundTrip(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-roundtrip"
	seed(t, store, userID)

	grantedAt := baseTime.Add(-time.Hour)
	expiresAt := baseTime.Add(30 * 24 * time.Hour)
	promo := &goquota.TierPromotion{
		Tier:           "premium",
		GrantedAt:      grantedAt,
		ExpiresAt:      expiresAt,
		Source:         "conformance",
		Reason:         "round_trip",
		IdempotencyKey: "conformance-roundtrip",
	}
	require.NoError(t, store.SetPromotion(ctx, userID, promo))

	got := mustGet(t, store, userID)
	require.NotNil(t, got.Promotion)
	assert.Equal(t, "premium", got.Promotion.Tier)
	assert.Equal(t, "conformance", got.Promotion.Source)
	assert.Equal(t, "round_trip", got.Promotion.Reason)
	assert.Equal(t, "conformance-roundtrip", got.Promotion.IdempotencyKey)
	assert.True(t, got.Promotion.GrantedAt.Equal(grantedAt),
		"grantedAt: got %s want %s", got.Promotion.GrantedAt, grantedAt)
	assert.True(t, got.Promotion.ExpiresAt.Equal(expiresAt),
		"expiresAt: got %s want %s", got.Promotion.ExpiresAt, expiresAt)

	// The overlay must never touch the provider-owned base fields.
	assert.Equal(t, "free", got.Tier, "base tier must be unchanged")
	assert.True(t, got.SubscriptionStartDate.Equal(baseTime), "base start date must be unchanged")

	// Mutating the caller's value must not affect the stored copy.
	promo.Tier = "mutated"
	got = mustGet(t, store, userID)
	require.NotNil(t, got.Promotion)
	assert.Equal(t, "premium", got.Promotion.Tier, "storage must not alias the caller's promotion")
}

func testReplacesExisting(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-replace"
	seed(t, store, userID)

	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(24 * time.Hour),
	}))
	secondExpiry := baseTime.Add(48 * time.Hour)
	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "pro", ExpiresAt: secondExpiry,
	}))

	got := mustGet(t, store, userID)
	require.NotNil(t, got.Promotion)
	assert.Equal(t, "pro", got.Promotion.Tier, "a later promotion must replace the previous one")
	assert.True(t, got.Promotion.ExpiresAt.Equal(secondExpiry))
}

func testMissingEntitlement(t *testing.T, store Store) {
	userID := userPrefix + "-missing"
	err := store.SetPromotion(context.Background(), userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(time.Hour),
	})
	assert.ErrorIs(t, err, goquota.ErrEntitlementNotFound,
		"granting a promotion to an identity with no entitlement must fail")

	_, err = store.GetEntitlement(context.Background(), userID)
	assert.ErrorIs(t, err, goquota.ErrEntitlementNotFound)
}

func testClearIsIdempotent(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-clear"
	seed(t, store, userID)
	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(time.Hour),
	}))

	require.NoError(t, store.ClearPromotion(ctx, userID))
	assert.Nil(t, mustGet(t, store, userID).Promotion)

	// Clearing again, and clearing an identity with no entitlement, are not errors.
	require.NoError(t, store.ClearPromotion(ctx, userID))
	require.NoError(t, store.ClearPromotion(ctx, userPrefix+"-clear-unknown"))
}

func testBaseWritePreserves(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-base-write"
	seed(t, store, userID)
	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(time.Hour),
	}))

	// A provider-style write constructs a fresh entitlement with no promotion.
	providerUpdatedAt := baseTime.Add(time.Minute)
	require.NoError(t, store.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: baseTime,
		UpdatedAt:             providerUpdatedAt,
	}))

	got := mustGet(t, store, userID)
	assert.Equal(t, "pro", got.Tier, "provider write must update the base tier")
	assert.True(t, got.UpdatedAt.Equal(providerUpdatedAt), "provider write must update UpdatedAt")
	require.NotNil(t, got.Promotion, "provider write must not erase the promotion")
	assert.Equal(t, "premium", got.Promotion.Tier)
}

func testClearPreservesBaseFields(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-clear-base"
	seed(t, store, userID)

	before := mustGet(t, store, userID)
	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(time.Hour),
	}))
	require.NoError(t, store.ClearPromotion(ctx, userID))

	after := mustGet(t, store, userID)
	assert.Nil(t, after.Promotion)
	assert.Equal(t, before.Tier, after.Tier)
	assert.True(t, after.SubscriptionStartDate.Equal(before.SubscriptionStartDate))
}

// testWritesLeaveUpdatedAtUntouched guards provider-webhook idempotency: providers
// skip events that are not newer than Entitlement.UpdatedAt, so a promotion write
// that bumps it can cause a legitimate, in-flight payment webhook to be dropped.
func testWritesLeaveUpdatedAtUntouched(t *testing.T, store Store) {
	ctx := context.Background()
	userID := userPrefix + "-updated-at"
	seed(t, store, userID)

	before := mustGet(t, store, userID)

	require.NoError(t, store.SetPromotion(ctx, userID, &goquota.TierPromotion{
		Tier: "premium", ExpiresAt: baseTime.Add(time.Hour),
	}))
	assert.True(t, mustGet(t, store, userID).UpdatedAt.Equal(before.UpdatedAt),
		"SetPromotion must not modify UpdatedAt")

	require.NoError(t, store.ClearPromotion(ctx, userID))
	assert.True(t, mustGet(t, store, userID).UpdatedAt.Equal(before.UpdatedAt),
		"ClearPromotion must not modify UpdatedAt")
}
