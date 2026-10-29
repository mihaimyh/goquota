package firestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestJoinDocID_IsInjective guards against the ambiguous underscore join:
// ("a_b", "c") and ("a", "b_c") used to map to the same "a_b_c" document.
func TestJoinDocID_IsInjective(t *testing.T) {
	assert.NotEqual(t, joinDocID("a_b", "c"), joinDocID("a", "b_c"))
	assert.NotEqual(t, joinDocID("a|b", "c"), joinDocID("a", "b|c"))
	assert.NotEqual(t, joinDocID("2026-01-01", "api_calls"), joinDocID("2026-01-01_api", "calls"))
}

// TestJoinDocID_KeepsReadableIDs asserts plain ids still produce stable,
// human-readable document ids.
func TestJoinDocID_KeepsReadableIDs(t *testing.T) {
	assert.Equal(t, "user1|api_calls", joinDocID("user1", "api_calls"))
	assert.Equal(t, "forever|credits", joinDocID("forever", "credits"))
	assert.Equal(t, "user1", joinDocID("user1"))
}

// TestFirestore_DocRefsAreInjective checks the helper that actually builds the
// rate-limit document reference. Requires the Firestore emulator (skipped
// otherwise).
func TestFirestore_DocRefsAreInjective(t *testing.T) {
	storage := newPromotionTestStorage(t, "doc_ids")

	assert.NotEqual(t,
		storage.rateLimitDoc("a_b", "c").ID,
		storage.rateLimitDoc("a", "b_c").ID,
	)
	assert.Equal(t, "user1|api_calls", storage.rateLimitDoc("user1", "api_calls").ID)

	// Usage documents keep the legacy id: the period key is constant per period,
	// so the underscore join cannot collide and re-keying would reset counters.
	period := goquota.Period{
		Type:  goquota.PeriodTypeMonthly,
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	assert.Equal(t, "2026-01-01_api_calls", storage.usageDoc("user1", "api_calls", period).ID)
}

// TestFirestore_SetEntitlement_ClearsExpiry covers FIRE-02: MergeAll keeps a
// stored field when the key is absent, so a rewrite without an expiry must
// remove it explicitly instead of silently downgrading the entitlement later.
func TestFirestore_SetEntitlement_ClearsExpiry(t *testing.T) {
	storage := newPromotionTestStorage(t, "expiry_clear")
	ctx := context.Background()

	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	ent := firestoreBaseEntitlement("u1", "premium")
	ent.ExpiresAt = &expires
	require.NoError(t, storage.SetEntitlement(ctx, ent))

	stored, err := storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	require.NotNil(t, stored.ExpiresAt)
	assert.True(t, stored.ExpiresAt.Equal(expires))

	// Provider-style write with no expiry: the stale timestamp must be gone.
	require.NoError(t, storage.SetEntitlement(ctx, firestoreBaseEntitlement("u1", "free")))

	stored, err = storage.GetEntitlement(ctx, "u1")
	require.NoError(t, err)
	assert.Nil(t, stored.ExpiresAt)

	// The field itself must be deleted, not stored as null.
	snap, err := storage.client.Collection(storage.entitlementsCollection).Doc("u1").Get(ctx)
	require.NoError(t, err)
	assert.NotContains(t, snap.Data(), "expiresAt")
}
