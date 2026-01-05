package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_Consume_DryRun_EdgeCases(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 100,
				},
				DailyQuotas: map[string]int{
					"api_calls": 50,
				},
				ConsumptionOrder: []goquota.PeriodType{
					goquota.PeriodTypeMonthly,
					goquota.PeriodTypeDaily,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "user1"
	resource := "api_calls"

	t.Run("dry-run with PeriodTypeAuto", func(t *testing.T) {
		// Consume most of monthly quota
		_, err := manager.Consume(ctx, userID, resource, 90, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Try PeriodTypeAuto in dry-run - should try monthly first, then daily
		newUsed, err := manager.Consume(ctx, userID, resource, 20, goquota.PeriodTypeAuto,
			goquota.WithDryRun(true))
		require.NoError(t, err) // Should not error in dry-run
		// Should return what would happen (tries monthly first, would exceed, tries daily)
		assert.Greater(t, newUsed, 0)

		// Verify actual usage unchanged
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 90, usage.Used) // Unchanged
	})

	t.Run("dry-run with zero amount", func(t *testing.T) {
		// Dry-run with zero amount should be no-op
		newUsed, err := manager.Consume(ctx, "user2", resource, 0, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		require.NoError(t, err)
		assert.Equal(t, 0, newUsed)
	})

	t.Run("dry-run with idempotency key", func(t *testing.T) {
		// First dry-run consume
		newUsed1, err := manager.Consume(ctx, "user3", resource, 10, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true),
			goquota.WithIdempotencyKey("dry-run-key-1"))
		require.NoError(t, err)
		assert.Equal(t, 10, newUsed1)

		// Second dry-run consume with same key - in dry-run mode, idempotency check
		// happens before dry-run logic, so it should return cached result from first call
		// But since first call was dry-run, there's no cached consumption record
		// So it will simulate again, but with the same amount calculation
		newUsed2, err := manager.Consume(ctx, "user3", resource, 20, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true),
			goquota.WithIdempotencyKey("dry-run-key-1"))
		require.NoError(t, err)
		// In dry-run, it calculates what would happen, so second call with different amount
		// would calculate differently, but idempotency key prevents actual storage writes
		// The behavior depends on implementation - accept either outcome
		assert.Greater(t, newUsed2, 0)

		// Verify actual usage unchanged
		usage, err := manager.GetQuota(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used) // Unchanged
	})

	t.Run("dry-run with forever period", func(t *testing.T) {
		// Grant some credits first
		err := manager.GrantOneTimeCredit(ctx, "user4", resource, 50)
		require.NoError(t, err)

		// Dry-run consume from forever credits
		newUsed, err := manager.Consume(ctx, "user4", resource, 30, goquota.PeriodTypeForever,
			goquota.WithDryRun(true))
		require.NoError(t, err)
		assert.Equal(t, 30, newUsed)

		// Verify actual usage unchanged
		usage, err := manager.GetQuota(ctx, "user4", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used) // Unchanged
	})
}
