package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_ConsumeWithResult_EdgeCases(t *testing.T) {
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
	resource := "api_calls"

	t.Run("consume with result for forever period", func(t *testing.T) {
		// Grant some forever credits
		err := manager.GrantOneTimeCredit(ctx, "user1", resource, 50)
		require.NoError(t, err)

		result, err := manager.ConsumeWithResult(ctx, "user1", resource, 20, goquota.PeriodTypeForever)
		require.NoError(t, err)

		assert.Equal(t, 20, result.NewUsed)
		assert.GreaterOrEqual(t, result.Limit, 50)
		assert.GreaterOrEqual(t, result.Remaining, 30)
	})

	t.Run("consume with result with zero limit", func(t *testing.T) {
		// User with no quota configured
		result, err := manager.ConsumeWithResult(ctx, "user2", "no_quota_resource", 10, goquota.PeriodTypeMonthly)
		// Should fail with quota exceeded
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, goquota.ErrQuotaExceeded, err)
	})

	t.Run("consume with result at exactly limit", func(t *testing.T) {
		// Consume exactly the limit
		result, err := manager.ConsumeWithResult(ctx, "user3", resource, 100, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 100, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 0, result.Remaining)
		assert.InDelta(t, 100.0, result.Percentage, 0.1)
	})

	t.Run("consume with result with very small amounts", func(t *testing.T) {
		// Consume 1 unit
		result, err := manager.ConsumeWithResult(ctx, "user4", resource, 1, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 1, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 99, result.Remaining)
		assert.InDelta(t, 1.0, result.Percentage, 0.1)
	})
}

func TestManager_GetUsageAfterConsume_EdgeCases(t *testing.T) {
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
	resource := "api_calls"

	t.Run("get usage after consume for forever period", func(t *testing.T) {
		// Grant some credits
		err := manager.GrantOneTimeCredit(ctx, "user1", resource, 50)
		require.NoError(t, err)

		usage, err := manager.GetUsageAfterConsume(ctx, "user1", resource, 20, goquota.PeriodTypeForever)
		require.NoError(t, err)

		assert.Equal(t, 20, usage.Used)
		assert.GreaterOrEqual(t, usage.Limit, 50)
	})

	t.Run("get usage after consume fails on quota exceeded", func(t *testing.T) {
		usage, err := manager.GetUsageAfterConsume(ctx, "user2", resource, 150, goquota.PeriodTypeMonthly)
		assert.Error(t, err)
		assert.Nil(t, usage)
		assert.Equal(t, goquota.ErrQuotaExceeded, err)
	})
}
