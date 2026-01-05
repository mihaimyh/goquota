package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
)

func TestManager_TimeSource_Memory(t *testing.T) {
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

	// Memory storage implements TimeSource - test directly
	serverTime, err := storage.Now(ctx)
	require.NoError(t, err)

	// Should be within a few seconds of local time
	localTime := time.Now().UTC()
	diff := serverTime.Sub(localTime)
	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 5*time.Second, "Server time should be close to local time")

	// Verify manager was created successfully
	_ = manager
}

func TestManager_TimeSource_Redis(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use DB 15 for testing
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clear test database
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test database: %v", err)
	}

	storage, err := redisStorage.New(client, redisStorage.DefaultConfig())
	require.NoError(t, err)

	// Redis storage implements TimeSource - test directly
	serverTime, err := storage.Now(ctx)
	require.NoError(t, err)

	// Should be within a few seconds of local time
	localTime := time.Now().UTC()
	diff := serverTime.Sub(localTime)
	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 5*time.Second, "Server time should be close to local time")

	// Test that Manager uses TimeSource
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

	// Consume should use storage time
	_, err = manager.Consume(ctx, "user1", "api_calls", 10, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
}
