package goquota_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

func TestConfig_Validate(t *testing.T) {
	t.Run("valid config passes", func(t *testing.T) {
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

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("missing default tier fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "defaultTier is required")
	})

	t.Run("default tier not in tiers map fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "premium",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist in Tiers map")
	})

	t.Run("negative quota fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": -10,
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "negative monthly quota")
	})

	t.Run("warning threshold out of range fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					WarningThresholds: map[string][]float64{
						"api_calls": {0.5, 1.5}, // 1.5 is out of range
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "warning threshold")
		assert.Contains(t, err.Error(), "out of range [0, 1]")
	})

	t.Run("invalid rate limit algorithm fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					RateLimits: map[string]goquota.RateLimitConfig{
						"api_calls": {
							Algorithm: "invalid_algorithm",
							Rate:      10,
							Window:    time.Second,
						},
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid rate limit algorithm")
	})

	t.Run("invalid period type in consumption order fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					ConsumptionOrder: []goquota.PeriodType{
						goquota.PeriodTypeMonthly,
						goquota.PeriodType("invalid"),
					},
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid period type")
	})

	t.Run("tier name mismatch fails", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "premium", // Mismatch
				},
			},
		}

		err := config.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mismatched name")
	})
}
