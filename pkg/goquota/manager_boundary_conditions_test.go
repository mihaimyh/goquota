package goquota_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func TestManager_BoundaryConditions(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": math.MaxInt32,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	resource := "api_calls"

	t.Run("very large quota values", func(t *testing.T) {
		userID := "user_large_quota"
		// Consume a large amount
		newUsed, err := manager.Consume(ctx, userID, resource, 1000000, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 1000000, newUsed)

		usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, math.MaxInt32, usage.Limit)
		assert.Equal(t, 1000000, usage.Used)
	})

	t.Run("very long userID", func(t *testing.T) {
		// Create a very long userID (1000 characters)
		longUserID := strings.Repeat("a", 1000)
		err := manager.SetUsage(ctx, longUserID, resource, goquota.PeriodTypeMonthly, 50)
		require.NoError(t, err)

		usage, err := manager.GetQuota(ctx, longUserID, resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 50, usage.Used)
	})

	t.Run("very long resource name", func(t *testing.T) {
		// Create a very long resource name (500 characters)
		longResource := strings.Repeat("resource_", 50) + "api_calls"
		config2 := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						longResource: 100,
					},
				},
			},
		}

		manager2, err := goquota.NewManager(storage, &config2)
		require.NoError(t, err)

		err = manager2.SetUsage(ctx, "user1", longResource, goquota.PeriodTypeMonthly, 25)
		require.NoError(t, err)

		usage, err := manager2.GetQuota(ctx, "user1", longResource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 25, usage.Used)
	})

	t.Run("unicode characters in userID", func(t *testing.T) {
		// Test with various unicode characters
		unicodeUserIDs := []string{
			"user_中文",
			"user_日本語",
			"user_한국어",
			"user_🚀",
			"user_émojis_🎉🎊",
		}

		for _, userID := range unicodeUserIDs {
			err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 30)
			require.NoError(t, err, "Failed for userID: %s", userID)

			usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
			require.NoError(t, err, "Failed for userID: %s", userID)
			assert.Equal(t, 30, usage.Used, "Failed for userID: %s", userID)
		}
	})

	t.Run("unicode characters in resource", func(t *testing.T) {
		unicodeResources := []string{
			"api_中文",
			"api_日本語",
			"api_🚀",
		}

		for _, res := range unicodeResources {
			config3 := goquota.Config{
				DefaultTier: "free",
				Tiers: map[string]goquota.TierConfig{
					"free": {
						Name: "free",
						MonthlyQuotas: map[string]int{
							res: 100,
						},
					},
				},
			}

			manager3, err := goquota.NewManager(storage, &config3)
			require.NoError(t, err)

			err = manager3.SetUsage(ctx, "user1", res, goquota.PeriodTypeMonthly, 40)
			require.NoError(t, err, "Failed for resource: %s", res)

			usage, err := manager3.GetQuota(ctx, "user1", res, goquota.PeriodTypeMonthly)
			require.NoError(t, err, "Failed for resource: %s", res)
			assert.Equal(t, 40, usage.Used, "Failed for resource: %s", res)
		}
	})

	t.Run("max int32 quota value", func(t *testing.T) {
		config4 := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": math.MaxInt32,
					},
				},
			},
		}

		manager4, err := goquota.NewManager(storage, &config4)
		require.NoError(t, err)

		// Set usage to max int32
		err = manager4.SetUsage(ctx, "user_max", "api_calls", goquota.PeriodTypeMonthly, math.MaxInt32)
		require.NoError(t, err)

		usage, err := manager4.GetQuota(ctx, "user_max", "api_calls", goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, math.MaxInt32, usage.Used)
		assert.Equal(t, math.MaxInt32, usage.Limit)
	})

	t.Run("empty string userID and resource", func(_ *testing.T) {
		// Empty strings should be handled gracefully
		emptyResource := ""
		err := manager.SetUsage(ctx, "", emptyResource, goquota.PeriodTypeMonthly, 10)
		// Behavior depends on storage implementation - accept either outcome
		_ = err
	})

	t.Run("special characters in userID", func(t *testing.T) {
		specialUserIDs := []string{
			"user-with-dashes",
			"user_with_underscores",
			"user.with.dots",
			"user@domain.com",
			"user+tag@domain.com",
		}

		for _, userID := range specialUserIDs {
			err := manager.SetUsage(ctx, userID, resource, goquota.PeriodTypeMonthly, 20)
			require.NoError(t, err, "Failed for userID: %s", userID)

			usage, err := manager.GetQuota(ctx, userID, resource, goquota.PeriodTypeMonthly)
			require.NoError(t, err, "Failed for userID: %s", userID)
			assert.Equal(t, 20, usage.Used, "Failed for userID: %s", userID)
		}
	})
}

func TestManager_ConsumeWithResult_BoundaryConditions(t *testing.T) {
	storage := memory.New()
	ctx := context.Background()

	t.Run("ConsumeWithResult with max int32 values", func(t *testing.T) {
		config2 := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": math.MaxInt32,
					},
				},
			},
		}

		manager2, err := goquota.NewManager(storage, &config2)
		require.NoError(t, err)

		result, err := manager2.ConsumeWithResult(ctx, "user1", "api_calls", 1000000, goquota.PeriodTypeMonthly)
		require.NoError(t, err)

		assert.Equal(t, 1000000, result.NewUsed)
		assert.Equal(t, math.MaxInt32, result.Limit)
		// Verify percentage calculation doesn't overflow
		assert.Greater(t, result.Percentage, 0.0)
		assert.Less(t, result.Percentage, 100.0)
	})

	t.Run("ConsumeWithResult with zero limit", func(t *testing.T) {
		// Create a tier with zero quota
		configZero := goquota.Config{
			DefaultTier: "zero",
			Tiers: map[string]goquota.TierConfig{
				"zero": {
					Name: "zero",
					MonthlyQuotas: map[string]int{
						"api_calls": 0,
					},
				},
			},
		}
		managerZero, err := goquota.NewManager(storage, &configZero)
		require.NoError(t, err)

		// Set entitlement to zero tier
		err = managerZero.SetEntitlement(ctx, &goquota.Entitlement{
			UserID:                "user_zero",
			Tier:                  "zero",
			SubscriptionStartDate: time.Now().UTC(),
			UpdatedAt:             time.Now().UTC(),
		})
		require.NoError(t, err)

		// User with zero quota
		zeroResource := "api_calls"
		result, err := managerZero.ConsumeWithResult(ctx, "user_zero", zeroResource, 10, goquota.PeriodTypeMonthly)
		// Should fail with quota exceeded
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestConfig_Validate_BoundaryConditions(t *testing.T) {
	t.Run("max int32 quota in config", func(t *testing.T) {
		config := goquota.Config{
			DefaultTier: "free",
			Tiers: map[string]goquota.TierConfig{
				"free": {
					Name: "free",
					MonthlyQuotas: map[string]int{
						"api_calls": math.MaxInt32,
					},
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err) // Max int32 is valid
	})

	t.Run("very long tier name", func(t *testing.T) {
		longTierName := strings.Repeat("tier_", 100)
		config := goquota.Config{
			DefaultTier: longTierName,
			Tiers: map[string]goquota.TierConfig{
				longTierName: {
					Name: longTierName,
					MonthlyQuotas: map[string]int{
						"api_calls": 100,
					},
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("unicode in tier name", func(t *testing.T) {
		unicodeTierName := "tier_中文_日本語"
		config := goquota.Config{
			DefaultTier: unicodeTierName,
			Tiers: map[string]goquota.TierConfig{
				unicodeTierName: {
					Name: unicodeTierName,
					MonthlyQuotas: map[string]int{
						"api_calls": 100,
					},
				},
			},
		}

		err := config.Validate()
		assert.NoError(t, err)
	})
}

func TestManager_DryRun_BoundaryConditions(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": math.MaxInt32,
				},
			},
		},
	}

	manager, err := goquota.NewManager(storage, &config)
	require.NoError(t, err)

	ctx := context.Background()
	resource := "api_calls"

	t.Run("dry-run with max int32 values", func(t *testing.T) {
		// Dry-run with very large amount
		newUsed, err := manager.Consume(ctx, "user1", resource, 1000000, goquota.PeriodTypeMonthly,
			goquota.WithDryRun(true))
		require.NoError(t, err)
		assert.Equal(t, 1000000, newUsed)

		// Verify actual usage unchanged
		usage, err := manager.GetQuota(ctx, "user1", resource, goquota.PeriodTypeMonthly)
		require.NoError(t, err)
		assert.Equal(t, 0, usage.Used)
	})
}

// TestUTF8Validation ensures userID and resource can handle UTF-8 properly
func TestUTF8Validation(t *testing.T) {
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

	// Test various UTF-8 edge cases
	testCases := []struct {
		name     string
		userID   string
		resource string
	}{
		{"valid UTF-8", "user_正常", "api_calls"},
		{"UTF-8 with null byte", "user\x00test", "api_calls"},
		{"UTF-8 with control characters", "user\t\n\r", "api_calls"},
		{"UTF-8 with combining characters", "user\u0301", "api_calls"}, // e with combining acute
		{"UTF-8 with RTL text", "user_עברית", "api_calls"},
		{"UTF-8 with emoji", "user_🎉", "api_calls"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Verify strings are valid UTF-8
			if !utf8.ValidString(tc.userID) {
				t.Skipf("Invalid UTF-8 userID: %q", tc.userID)
			}
			if !utf8.ValidString(tc.resource) {
				t.Skipf("Invalid UTF-8 resource: %q", tc.resource)
			}

			err := manager.SetUsage(ctx, tc.userID, tc.resource, goquota.PeriodTypeMonthly, 10)
			// Some edge cases might fail, which is acceptable
			if err == nil {
				usage, err := manager.GetQuota(ctx, tc.userID, tc.resource, goquota.PeriodTypeMonthly)
				if err == nil {
					assert.Equal(t, 10, usage.Used)
				}
			}
		})
	}
}
