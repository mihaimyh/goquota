package redis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// TestStorage_SetEntitlement_PreservesTimezone covers H1: the timezone is a user
// preference no provider writes, so a provider update must not reset daily
// windows to UTC.
func TestStorage_SetEntitlement_PreservesTimezone(t *testing.T) {
	client := setupTestRedis(t)
	storage, err := New(client, DefaultConfig())
	require.NoError(t, err)

	ctx := context.Background()
	base := func(tier string) *goquota.Entitlement {
		return &goquota.Entitlement{
			UserID:                "user-1",
			Tier:                  tier,
			SubscriptionStartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}
	}

	zone := base("free")
	zone.Timezone = "Europe/Bucharest"
	require.NoError(t, storage.SetEntitlement(ctx, zone))

	// Provider webhook writes a plain entitlement (no timezone).
	require.NoError(t, storage.SetEntitlement(ctx, base("pro")))

	stored, err := storage.GetEntitlement(ctx, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "pro", stored.Tier)
	assert.Equal(t, "Europe/Bucharest", stored.Timezone, "timezone must survive a provider write")

	// An explicit timezone still wins.
	explicit := base("pro")
	explicit.Timezone = "America/New_York"
	require.NoError(t, storage.SetEntitlement(ctx, explicit))
	stored, err = storage.GetEntitlement(ctx, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "America/New_York", stored.Timezone)
}
