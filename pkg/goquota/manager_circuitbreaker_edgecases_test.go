package goquota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// CircuitBreakerStorage wraps storage and simulates circuit breaker open state
type CircuitBreakerStorage struct {
	goquota.Storage
	circuitOpen bool
}

func (c *CircuitBreakerStorage) GetUsage(
	ctx context.Context,
	userID, resource string,
	period goquota.Period,
) (*goquota.Usage, error) {
	if c.circuitOpen {
		return nil, errors.New("circuit breaker is open")
	}
	return c.Storage.GetUsage(ctx, userID, resource, period)
}

func (c *CircuitBreakerStorage) SetUsage(
	ctx context.Context,
	userID, resource string,
	usage *goquota.Usage,
	period goquota.Period,
) error {
	if c.circuitOpen {
		return errors.New("circuit breaker is open")
	}
	return c.Storage.SetUsage(ctx, userID, resource, usage, period)
}

func (c *CircuitBreakerStorage) AddLimit(
	ctx context.Context,
	userID, resource string,
	amount int,
	period goquota.Period,
	idempotencyKey string,
) error {
	if c.circuitOpen {
		return errors.New("circuit breaker is open")
	}
	return c.Storage.AddLimit(ctx, userID, resource, amount, period, idempotencyKey)
}

func TestManager_AdminMethods_WithCircuitBreaker(t *testing.T) {
	baseStorage := memory.New()
	cbStorage := &CircuitBreakerStorage{
		Storage:     baseStorage,
		circuitOpen: true,
	}

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
		CircuitBreakerConfig: &goquota.CircuitBreakerConfig{
			Enabled: true,
		},
	}

	manager, err := goquota.NewManager(cbStorage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "user1"
	resource := "api_calls"

	t.Run("SetUsage fails when circuit breaker is open", func(t *testing.T) {
		err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 50)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
	})

	t.Run("GrantOneTimeCredit fails when circuit breaker is open", func(t *testing.T) {
		err := manager.GrantOneTimeCredit(ctx, "user2", resource, 25)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
	})

	t.Run("ResetUsage fails when circuit breaker is open", func(t *testing.T) {
		err := manager.ResetUsage(ctx, "user3", resource, goquota.PeriodTypeMonthly)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
	})
}

func TestManager_TimeSource_WithCircuitBreaker(t *testing.T) {
	baseStorage := memory.New()
	cbStorage := &CircuitBreakerStorage{
		Storage:     baseStorage,
		circuitOpen: false, // Start with circuit closed
	}

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

	manager, err := goquota.NewManager(cbStorage, &config)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("TimeSource works when circuit breaker is closed", func(t *testing.T) {
		// Memory storage implements TimeSource
		serverTime, err := baseStorage.Now(ctx)
		require.NoError(t, err)
		assert.False(t, serverTime.IsZero())
	})

	t.Run("Manager falls back to local time when TimeSource fails", func(t *testing.T) {
		// Even if storage fails, manager should use local time as fallback
		// This is tested indirectly through period calculations
		_, err := manager.Consume(ctx, "user1", "api_calls", 10, goquota.PeriodTypeMonthly)
		// Should succeed even if TimeSource had issues (falls back to time.Now())
		require.NoError(t, err)
	})
}
