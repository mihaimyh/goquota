//go:build integration
// +build integration

package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

func setupTestRedisForIntegration(t *testing.T) *redis.Client {
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

func TestManager_AdminMethods_Concurrent_Redis(t *testing.T) {
	client := setupTestRedisForIntegration(t)
	defer client.Close()

	storage, err := redisStorage.New(client, redisStorage.DefaultConfig())
	require.NoError(t, err)

	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	resource := "api_calls"

	t.Run("concurrent SetUsage operations", func(t *testing.T) {
		// This tests that SetUsage is safe for concurrent access
		// (though in practice, admin operations should be serialized)
		userID := "concurrent_user"
		done := make(chan bool, 2)

		go func() {
			err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
			assert.NoError(t, err)
			done <- true
		}()

		go func() {
			err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 75)
			assert.NoError(t, err)
			done <- true
		}()

		// Wait for both to complete
		<-done
		<-done

		// Verify final state (should be one of the values)
		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.True(t, usage.Used == 50 || usage.Used == 75)
	})
}

func TestManager_DryRun_WithRateLimit_Redis(t *testing.T) {
	client := setupTestRedisForIntegration(t)
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
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     10,
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "user1"
	resource := "api_calls"

	t.Run("dry-run still checks rate limits", func(t *testing.T) {
		// Dry-run mode should still respect rate limits
		// (rate limits are checked before dry-run logic)
		// Exhaust rate limit first
		for i := 0; i < 10; i++ {
			_, err := manager.Consume(ctx, userID, resource, 1, goquota.PeriodTypeMonthly)
			require.NoError(t, err)
		}

		// Wait a bit for rate limit window
		time.Sleep(100 * time.Millisecond)

		// Now try dry-run - should still be rate limited
		_, err := manager.Consume(ctx, userID, resource, 1, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		// Rate limit should still apply even in dry-run
		assert.Error(t, err)
		// Should be rate limit error, not quota exceeded
		_, ok := err.(*goquota.RateLimitExceededError)
		assert.True(t, ok, "Should be rate limit error")
	})
}
