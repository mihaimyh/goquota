package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_ConsumeWithResult(t *testing.T) {
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

	t.Run("consume with result returns full information", func(t *testing.T) {
		result, err := manager.ConsumeWithResult(ctx, userID, resource, 30, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 30, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 70, result.Remaining)
		assert.InDelta(t, 30.0, result.Percentage, 0.1) // 30%
	})

	t.Run("consume with result shows correct percentage", func(t *testing.T) {
		// Reset first
		err := manager.ResetUsage(ctx, "user2", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Consume 50%
		result, err := manager.ConsumeWithResult(ctx, "user2", resource, 50, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 50, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 50, result.Remaining)
		assert.InDelta(t, 50.0, result.Percentage, 0.1) // 50%
	})

	t.Run("consume with result at limit", func(t *testing.T) {
		// Reset first
		err := manager.ResetUsage(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Consume all
		result, err := manager.ConsumeWithResult(ctx, "user3", resource, 100, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 100, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 0, result.Remaining)
		assert.InDelta(t, 100.0, result.Percentage, 0.1) // 100%
	})

	t.Run("consume with result fails on quota exceeded", func(t *testing.T) {
		// Reset first
		err := manager.ResetUsage(ctx, "user4", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Consume more than limit
		result, err := manager.ConsumeWithResult(ctx, "user4", resource, 150, goquota.PeriodTypeMonthly)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, goquota.ErrQuotaExceeded, err)
	})
}

func TestManager_GetUsageAfterConsume(t *testing.T) {
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

	t.Run("get usage after consume returns updated usage", func(t *testing.T) {
		usage, err := manager.GetUsageAfterConsume(ctx, userID, resource, 25, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 25, usage.Used)
		assert.Equal(t, 100, usage.Limit)
	})

	t.Run("get usage after consume fails on quota exceeded", func(t *testing.T) {
		// Reset first
		err := manager.ResetUsage(ctx, "user2", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Try to consume more than limit
		usage, err := manager.GetUsageAfterConsume(ctx, "user2", resource, 150, goquota.PeriodTypeMonthly)
		assert.Error(t, err)
		assert.Nil(t, usage)
		assert.Equal(t, goquota.ErrQuotaExceeded, err)
	})
}
