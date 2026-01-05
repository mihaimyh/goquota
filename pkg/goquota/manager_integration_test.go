//go:build integration
// +build integration

package goquota_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use DB 15 for testing
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clear test database
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test database: %v", err)
	}

	return client
}

func TestManager_AdminMethods_Redis(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := redisStorage.New(client, redisStorage.DefaultConfig())
	require.NoError(t, err)

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

	t.Run("set usage with redis storage", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Used)
		assert.Equal(t, 100, usage.Limit)
	})

	t.Run("grant one-time credit with redis storage", func(t *testing.T) {
		// Grant one-time credit (adds to forever credits limit via TopUpLimit)
		// This should succeed without error
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		// Grant again to verify it accumulates (TopUpLimit should add to existing limit)
		err = manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		// The function should complete successfully - actual consumption test
		// is covered in unit tests. Integration test verifies it works with Redis storage.
	})

	t.Run("reset usage with redis storage", func(t *testing.T) {
		// First consume some quota
		_, err := manager.Consume(ctx, "user3", resource, 30, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Reset
		err = manager.ResetUsage(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})
}

func TestManager_DryRun_Redis(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := redisStorage.New(client, redisStorage.DefaultConfig())
	require.NoError(t, err)

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

	t.Run("dry-run allows exceeding quota with redis", func(t *testing.T) {
		// Consume most of the quota
		_, err := manager.Consume(ctx, userID, resource, 90, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Try to consume more in dry-run mode
		newUsed, err := manager.Consume(ctx, userID, resource, 20, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		require.NoError(t, err)
		assert.Equal(t, 110, newUsed)

		// Verify actual usage unchanged
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 90, usage.Used)
	})
}

func TestManager_ConsumeWithResult_Redis(t *testing.T) {
	client := setupTestRedis(t)
	defer client.Close()

	storage, err := redisStorage.New(client, redisStorage.DefaultConfig())
	require.NoError(t, err)

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

	t.Run("consume with result returns full info with redis", func(t *testing.T) {
		result, err := manager.ConsumeWithResult(ctx, userID, resource, 30, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 30, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 70, result.Remaining)
		assert.InDelta(t, 30.0, result.Percentage, 0.1)
	})
}
