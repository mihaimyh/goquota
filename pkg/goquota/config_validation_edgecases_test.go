package goquota_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestConfig_Validate_EdgeCases(t *testing.T) {
	t.Run("empty tiers map", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers:       map[string]goquota.TierConfig{},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist in Tiers map")
	})

	t.Run("nil tiers map", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers:       nil,
		}

		err := config.Validate()
		// Nil tiers map means default tier won't exist, so should error
		assert.Error(t, err)
		// Should either error on missing default tier or handle nil gracefully
		if err != nil {
			assert.Contains(t, err.Error(), "does not exist in Tiers map")
		}
	})

	t.Run("zero quota values", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": 0, // Zero is valid
					},
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Zero quota is valid
	})

	t.Run("very large quota values", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": 2147483647, // Max int32
					},
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Large values are valid
	})

	t.Run("empty tier name", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "", // Empty name
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mismatched name")
	})

	t.Run("nil cache config", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			CacheConfig: nil, // Nil is valid
		}

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("cache config with zero values", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			CacheConfig: &goquota.CacheConfig{
				Enabled:         true,
				EntitlementTTL:  0, // Zero is valid
				UsageTTL:        0,
				MaxEntitlements: 0,
				MaxUsage:        0,
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Zero values are valid
	})

	t.Run("circuit breaker config with zero values", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			CircuitBreakerConfig: &goquota.CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 0, // Zero is valid
				ResetTimeout:     0,
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Zero values are valid
	})

	t.Run("fallback config with zero percentage", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			FallbackConfig: &goquota.FallbackConfig{
				Enabled:                       true,
				OptimisticAllowancePercentage: 0, // Zero is valid
				MaxStaleness:                  0,
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Zero percentage is valid
	})

	t.Run("fallback config with 100 percent", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			FallbackConfig: &goquota.FallbackConfig{
				Enabled:                       true,
				OptimisticAllowancePercentage: 100, // 100% is valid
			},
		}

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("fallback config with over 100 percent fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
			FallbackConfig: &goquota.FallbackConfig{
				Enabled:                       true,
				OptimisticAllowancePercentage: 101, // Over 100% is invalid
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be in range [0, 100]")
	})

	t.Run("idempotency key TTL zero", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier:       "free",
			IdempotencyKeyTTL: 0, // Zero is valid
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("idempotency key TTL negative fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier:       "free",
			IdempotencyKeyTTL: -1 * time.Hour, // Negative is invalid
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be negative")
	})

	t.Run("rate limit with zero window fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					RateLimits: map[string]goquota.RateLimitConfig{
						"api_calls": {
							Rate:      10,
							Window:    0, // Zero window is invalid
							Burst:     10,
							Algorithm: "token_bucket",
						},
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid rate limit window")
	})

	t.Run("rate limit with negative burst fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					RateLimits: map[string]goquota.RateLimitConfig{
						"api_calls": {
							Rate:      10,
							Window:    time.Second,
							Burst:     -1, // Negative burst is invalid
							Algorithm: "token_bucket",
						},
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "negative burst limit")
	})
}
