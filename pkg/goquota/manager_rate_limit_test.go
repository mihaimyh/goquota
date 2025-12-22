package goquota_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_Consume_WithRateLimit_Allowed(t *testing.T) {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     20,
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// First request should be allowed
	newUsed, err := manager.Consume(ctx, "user1", "api_calls", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 1, newUsed)
}

func TestManager_Consume_WithRateLimit_Exceeded(t *testing.T) {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     10, // Small burst for testing
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user2",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Consume all rate limit tokens
	for i := 0; i < 10; i++ {
		_, err := manager.Consume(ctx, "user2", "api_calls", 1, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
	}

	// 11th request should be rate limited
	_, err = manager.Consume(ctx, "user2", "api_calls", 1, goquota.PeriodTypeMonthly)
	assert.Error(t, err)
	var rateLimitErr *goquota.RateLimitExceededError
	assert.True(t, errors.As(err, &rateLimitErr))
	assert.NotNil(t, rateLimitErr.Info)
}

func TestManager_Consume_WithRateLimit_NoConfig(t *testing.T) {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				// No rate limits configured
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user3",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Should work without rate limiting
	newUsed, err := manager.Consume(ctx, "user3", "api_calls", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 1, newUsed)
}

func TestManager_Consume_WithRateLimit_SlidingWindow(t *testing.T) {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "sliding_window",
						Rate:      10,
						Window:    time.Minute,
						Burst:     0,
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user4",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Make 10 requests (all should be allowed)
	for i := 0; i < 10; i++ {
		_, err := manager.Consume(ctx, "user4", "api_calls", 1, goquota.PeriodTypeMonthly)
		require.NoError(t, err, "should be allowed for request %d", i)
	}

	// 11th request should be rate limited
	_, err = manager.Consume(ctx, "user4", "api_calls", 1, goquota.PeriodTypeMonthly)
	assert.Error(t, err)
	var rateLimitErr *goquota.RateLimitExceededError
	assert.True(t, errors.As(err, &rateLimitErr))
}

func TestManager_Consume_WithRateLimit_StorageError_GracefulDegradation(t *testing.T) {
	// Create a storage that fails on rate limit check
	failingStorage := newMockFailingRateLimitStorage(errors.New("storage error"))

	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     20,
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(failingStorage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user5",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Should allow request despite storage error (graceful degradation)
	newUsed, err := manager.Consume(ctx, "user5", "api_calls", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 1, newUsed)
}

func TestManager_Consume_WithRateLimit_DifferentResources(t *testing.T) {
	storage := memory.New()
	config := &goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls":    1000,
					"file_uploads": 100,
				},
				RateLimits: map[string]goquota.RateLimitConfig{
					"api_calls": {
						Algorithm: "token_bucket",
						Rate:      10,
						Window:    time.Second,
						Burst:     10,
					},
					"file_uploads": {
						Algorithm: "token_bucket",
						Rate:      5,
						Window:    time.Second,
						Burst:     5,
					},
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	require.NoError(t, err)

	ctx := context.Background()
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user6",
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
	})

	// Consume all api_calls rate limit
	for i := 0; i < 10; i++ {
		_, err := manager.Consume(ctx, "user6", "api_calls", 1, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
	}

	// file_uploads should still have rate limit available
	newUsed, err := manager.Consume(ctx, "user6", "file_uploads", 1, goquota.PeriodTypeMonthly)
	require.NoError(t, err)
	assert.Equal(t, 1, newUsed)
}

// mockFailingRateLimitStorage is a storage that fails on rate limit operations
type mockFailingRateLimitStorage struct {
	goquota.Storage
	checkRateLimitErr error
}

func newMockFailingRateLimitStorage(checkRateLimitErr error) *mockFailingRateLimitStorage {
	return &mockFailingRateLimitStorage{
		Storage:           memory.New(),
		checkRateLimitErr: checkRateLimitErr,
	}
}

//nolint:gocritic // Named return values would reduce readability here
func (m *mockFailingRateLimitStorage) CheckRateLimit(
	_ context.Context, _ *goquota.RateLimitRequest,
) (bool, int, time.Time, error) {
	return false, 0, time.Time{}, m.checkRateLimitErr
}

func (m *mockFailingRateLimitStorage) RecordRateLimitRequest(_ context.Context, _ *goquota.RateLimitRequest) error {
	return nil
}
