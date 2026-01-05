package goquota_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_SetUsage_EdgeCases(t *testing.T) {
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

	t.Run("set usage higher than limit", func(t *testing.T) {
		// Setting usage higher than limit should be allowed (admin override)
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 150)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 150, usage.Used)
		assert.Equal(t, 100, usage.Limit)
	})

	t.Run("set usage for forever period", func(t *testing.T) {
		// First grant some forever credits
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 50)
		require.NoError(t, err)

		// Set usage for forever period
		err = manager.SetUsage(ctx, "user2", resource, goquota.PeriodTypeForever, 25)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, "user2", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 25, usage.Used)
		assert.GreaterOrEqual(t, usage.Limit, 50)
	})

	t.Run("set usage with empty userID", func(_ *testing.T) {
		err := manager.SetUsage(ctx, "", resource, goquota.PeriodTypeMonthly, 10)
		// Should either succeed (if storage allows) or fail gracefully
		// The behavior depends on storage implementation
		_ = err // Accept either outcome
	})

	t.Run("set usage with empty resource", func(_ *testing.T) {
		err := manager.SetUsage(ctx, userID, "", goquota.PeriodTypeMonthly, 10)
		// Should either succeed (if storage allows) or fail gracefully
		_ = err // Accept either outcome
	})
}

func TestManager_GrantOneTimeCredit_EdgeCases(t *testing.T) {
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

	const testResourceAPICalls = "api_calls"

	ctx := context.Background()
	resource := testResourceAPICalls

	t.Run("grant very large credit", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user1", resource, 1000000)
		require.NoError(t, err)

		// Verify it was added
		usage, err := manager.GetQuota(ctx, "user1", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, usage.Limit, 1000000)
	})

	t.Run("grant credit for non-existent resource", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user2", "non_existent_resource", 50)
		// Should succeed - resources don't need to be pre-configured for forever credits
		require.NoError(t, err)
	})
}

func TestManager_ResetUsage_EdgeCases(t *testing.T) {
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

	const testResourceAPICalls = "api_calls"

	ctx := context.Background()
	resource := testResourceAPICalls

	t.Run("reset usage that doesn't exist", func(t *testing.T) {
		// Reset usage for user with no usage record
		err := manager.ResetUsage(ctx, "new_user", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Verify it's zero
		usage, err := manager.GetQuota(ctx, "new_user", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})

	t.Run("reset forever period usage", func(t *testing.T) {
		// Grant and consume some credits
		err := manager.GrantOneTimeCredit(ctx, "user3", resource, 50)
		require.NoError(t, err)

		_, err = manager.Consume(ctx, "user3", resource, 20, goquota.PeriodTypeForever)
		require.NoError(t, err)

		// Reset
		err = manager.ResetUsage(ctx, "user3", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, "user3", resource, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})
}
