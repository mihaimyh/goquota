package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_SetUsage(t *testing.T) {
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

	const (
		testUserID           = "user1"
		testResourceAPICalls = "api_calls"
	)

	ctx := context.Background()
	userID := testUserID
	resource := testResourceAPICalls

	t.Run("set usage for monthly period", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Used)
		assert.Equal(t, 100, usage.Limit)
	})

	t.Run("set usage for daily period", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeDaily, 25)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeDaily)
		require.NoError(t, err)
		assert.Equal(t, 25, usage.Used)
	})

	t.Run("set usage to zero", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 0)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})

	t.Run("set usage with negative amount fails", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, -10)
		assert.Error(t, err)
		assert.Equal(t, goquota.ErrInvalidAmount, err)
	})
}

func TestManager_GrantOneTimeCredit(t *testing.T) {
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

	const (
		testUserID           = "user1"
		testResourceAPICalls = "api_calls"
	)

	ctx := context.Background()
	userID := testUserID
	resource := testResourceAPICalls

	t.Run("grant one-time credit", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, userID, resource, 50)
		require.NoError(t, err)

		// Check forever credits (one-time credits are stored as forever credits)
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Limit)
		assert.Equal(t, 0, usage.Used)
	})

	t.Run("grant multiple one-time credits accumulates", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		err = manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, "user2", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Limit)
	})

	t.Run("grant zero credit fails", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, userID, resource, 0)
		assert.Error(t, err)
		assert.Equal(t, goquota.ErrInvalidAmount, err)
	})

	t.Run("grant negative credit fails", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, userID, resource, -10)
		assert.Error(t, err)
		assert.Equal(t, goquota.ErrInvalidAmount, err)
	})
}

func TestManager_ResetUsage(t *testing.T) {
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
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	const (
		testUserID           = "user1"
		testResourceAPICalls = "api_calls"
	)

	ctx := context.Background()
	userID := testUserID
	resource := testResourceAPICalls

	t.Run("reset usage after consumption", func(t *testing.T) {
		// First consume some quota
		_, err := manager.Consume(ctx, userID, resource, 30, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Verify usage
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 30, usage.Used)

		// Reset usage
		err = manager.ResetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Verify reset
		usage, err = manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})

	t.Run("reset daily usage", func(t *testing.T) {
		_, err := manager.Consume(ctx, "user2", resource, 20, goquota.PeriodTypeDaily)
		require.NoError(t, err)

		err = manager.ResetUsage(ctx, "user2", resource, goquota.PeriodTypeDaily)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, "user2", resource, goquota.PeriodTypeDaily)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})
}
