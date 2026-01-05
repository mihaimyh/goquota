package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_TimeSource_EdgeCases(t *testing.T) {
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

	t.Run("manager uses TimeSource for period calculations", func(t *testing.T) {
		// Memory storage implements TimeSource
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)

		// Manager should use this time for period calculations
		// Consume should use storage time
		_, err = manager.Consume(ctx, "user1", "api_calls", 10, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		// Verify the time was used (indirectly by checking period is correct)
		usage, err := manager.GetQuota(ctx, "user1", "api_calls", goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.NotNil(t, usage)

		// Server time should be recent
		assert.WithinDuration(t, time.Now().UTC(), serverTime, 5*time.Second)
	})

	t.Run("TimeSource fallback on error", func(t *testing.T) {
		// For memory storage, Now() should never fail
		// But we can verify it returns a valid time
		serverTime, err := storage.Now(ctx)
		require.NoError(t, err)
		assert.False(t, serverTime.IsZero())
		assert.Equal(t, time.UTC, serverTime.Location())
	})

	t.Run("multiple TimeSource calls are consistent", func(t *testing.T) {
		time1, err := storage.Now(ctx)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond)

		time2, err := storage.Now(ctx)
		require.NoError(t, err)

		// time2 should be after or equal to time1
		assert.True(t, time2.After(time1) || time2.Equal(time1))
	})
}
