package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_Consume_DryRun(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "user1"
	resource := "api_calls"

	t.Run("dry-run allows request that would exceed quota", func(t *testing.T) {
		// Consume most of the quota first
		_, err := manager.Consume(ctx, userID, resource, 90, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Try to consume more than remaining in dry-run mode
		newUsed, err := manager.Consume(ctx, userID, resource, 20, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		require.NoError(t, err)       // Should not error in dry-run mode
		assert.Equal(t, 110, newUsed) // Returns what would be the new used amount

		// Verify actual usage wasn't changed
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 90, usage.Used) // Actual usage unchanged
	})

	t.Run("dry-run allows request that would succeed", func(t *testing.T) {
		// Reset usage
		err := manager.ResetUsage(ctx, "user2", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Try to consume in dry-run mode
		newUsed, err := manager.Consume(ctx, "user2", resource, 50, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		require.NoError(t, err)
		assert.Equal(t, 50, newUsed)

		// Verify actual usage wasn't changed
		usage, err := manager.GetQuota(ctx, "user2", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used) // Actual usage unchanged
	})

	t.Run("dry-run with idempotency key", func(t *testing.T) {
		// Reset usage
		err := manager.ResetUsage(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// First dry-run consume
		newUsed1, err := manager.Consume(ctx, "user3", resource, 30, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true),
			goquota.WithIdempotencyKey("dry-run-key-1"))
		require.NoError(t, err)
		assert.Equal(t, 30, newUsed1)

		// Second dry-run consume with same idempotency key (should return same result)
		newUsed2, err := manager.Consume(ctx, "user3", resource, 30, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true),
			goquota.WithIdempotencyKey("dry-run-key-1"))
		require.NoError(t, err)
		assert.Equal(t, 30, newUsed2)

		// Verify actual usage wasn't changed
		usage, err := manager.GetQuota(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})
}
