//go:build integration
// +build integration

package goquota_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	postgresStorage "github.com/mihaimyh/goquota/storage/postgres"
)

func setupTestPostgresForIntegration(t *testing.T) *postgresStorage.Storage {
	t.Helper()

	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/goquota_test?sslmode=disable"
	}

	ctx := context.Background()
	config := postgresStorage.DefaultConfig()
	config.ConnectionString = dsn
	config.CleanupEnabled = false // Disable cleanup in tests

	storage, err := postgresStorage.New(ctx, config)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	return storage
}

func TestManager_AdminMethods_Postgres(t *testing.T) {
	storage := setupTestPostgresForIntegration(t)
	defer storage.Close()

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

	t.Run("set usage with postgres storage", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Used)
		assert.Equal(t, 100, usage.Limit)
	})

	t.Run("grant one-time credit with postgres storage", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		require.NoError(t, err)

		// Verify it was added (check by trying to consume)
		newUsed, err := manager.Consume(ctx, "user2", resource, 20, goquota.PeriodTypeForever)
		require.NoError(t, err)
		assert.Equal(t, 20, newUsed)
	})

	t.Run("reset usage with postgres storage", func(t *testing.T) {
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

func TestManager_DryRun_Postgres(t *testing.T) {
	storage := setupTestPostgresForIntegration(t)
	defer storage.Close()

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

	t.Run("dry-run allows exceeding quota with postgres", func(t *testing.T) {
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

func TestManager_ConsumeWithResult_Postgres(t *testing.T) {
	storage := setupTestPostgresForIntegration(t)
	defer storage.Close()

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

	t.Run("consume with result returns full info with postgres", func(t *testing.T) {
		result, err := manager.ConsumeWithResult(ctx, userID, resource, 30, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 30, result.NewUsed)
		assert.Equal(t, 100, result.Limit)
		assert.Equal(t, 70, result.Remaining)
		assert.InDelta(t, 30.0, result.Percentage, 0.1)
	})
}
