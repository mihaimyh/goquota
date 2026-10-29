package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStorage_SetEntitlement_PreservesMergeTombstone guards ISM-03: a provider
// write of a plain entitlement used to overwrite Sealed/MigratedTo with zero
// values, un-sealing a merged user.
func TestStorage_SetEntitlement_PreservesMergeTombstone(t *testing.T) {
	storage := New()
	ctx := context.Background()
	expireAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	sealed := baseEntitlement("old-user", "free")
	sealed.Sealed = true
	sealed.MigratedTo = "new-user"
	sealed.ExpireAt = &expireAt
	require.NoError(t, storage.SetEntitlement(ctx, sealed))

	// Provider webhook writes a plain entitlement (no tombstone fields).
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("old-user", "pro")))

	stored, err := storage.GetEntitlement(ctx, "old-user")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier, "provider-owned fields must still be updated")
	assert.True(t, stored.Sealed, "sealed tombstone must survive a provider write")
	assert.Equal(t, "new-user", stored.MigratedTo, "migration target must survive a provider write")
	require.NotNil(t, stored.ExpireAt, "tombstone expiry must survive a provider write")
	assert.True(t, expireAt.Equal(*stored.ExpireAt))
	assert.True(t, stored.IsSealed(time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)))
}

// TestStorage_SetEntitlement_PreservesTimezone covers H1: the timezone is a user
// preference no provider writes, so a provider update must not reset daily
// windows to UTC.
func TestStorage_SetEntitlement_PreservesTimezone(t *testing.T) {
	storage := New()
	ctx := context.Background()

	zone := baseEntitlement("user-1", "free")
	zone.Timezone = "Europe/Bucharest"
	require.NoError(t, storage.SetEntitlement(ctx, zone))

	// Provider webhook writes a plain entitlement (no timezone).
	require.NoError(t, storage.SetEntitlement(ctx, baseEntitlement("user-1", "pro")))

	stored, err := storage.GetEntitlement(ctx, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	assert.Equal(t, "Europe/Bucharest", stored.Timezone, "timezone must survive a provider write")

	// An explicit timezone still wins.
	explicit := baseEntitlement("user-1", "pro")
	explicit.Timezone = "America/New_York"
	require.NoError(t, storage.SetEntitlement(ctx, explicit))
	stored, err = storage.GetEntitlement(ctx, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "America/New_York", stored.Timezone)
}
