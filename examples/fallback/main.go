package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// 1. Setup primary storage (simulating a potentially unreliable storage)
	primaryStorage := memory.New()

	// 2. Setup secondary storage for fallback
	secondaryStorage := memory.New()

	// 3. Configure Manager with fallback strategies enabled
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
			},
			"pro": {
				Name:          "pro",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:        true,
			EntitlementTTL: 5 * time.Minute,
			UsageTTL:       30 * time.Second,
		},
		FallbackConfig: &goquota.FallbackConfig{
			Enabled:                    true,
			FallbackToCache:            true,
			OptimisticAllowance:        true,
			OptimisticAllowancePercentage: 10.0, // Allow up to 10% optimistically
			SecondaryStorage:           secondaryStorage,
			MaxStaleness:               5 * time.Minute,
		},
	}

	manager, err := goquota.NewManager(primaryStorage, &config)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	userID := "user_123"

	// 4. Set up an initial entitlement in primary storage
	fmt.Println("Setting up entitlement for user_123 in primary storage...")
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		fmt.Printf("Error setting entitlement: %v\n", err)
	}

	// 5. Consume some quota to populate cache
	fmt.Println("\n--- Populating Cache ---")
	for i := 0; i < 5; i++ {
		_, err := manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeMonthly)
		if err != nil {
			fmt.Printf("Error consuming quota: %v\n", err)
		} else {
			fmt.Printf("Consumed 10 API calls (iteration %d)\n", i+1)
		}
	}

	// 6. Get current quota (this will be cached)
	fmt.Println("\n--- Getting Quota (cached) ---")
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		fmt.Printf("Error getting quota: %v\n", err)
	} else {
		fmt.Printf("Current usage: %d / %d\n", usage.Used, usage.Limit)
	}

	// 7. Demonstrate fallback to cache
	// Note: In a real scenario, you would simulate storage failure
	// For this example, we'll just show that fallback is configured
	fmt.Println("\n--- Fallback Configuration ---")
	fmt.Println("Fallback strategies are configured:")
	fmt.Println("  - Cache fallback: Enabled")
	fmt.Println("  - Optimistic allowance: Enabled (10%)")
	fmt.Println("  - Secondary storage: Configured")
	fmt.Println("\nWhen primary storage fails, the system will:")
	fmt.Println("  1. Try to use cached data (if fresh enough)")
	fmt.Println("  2. Fall back to secondary storage")
	fmt.Println("  3. Allow optimistic consumption (up to 10% of limit)")

	// 8. Demonstrate optimistic consumption scenario
	fmt.Println("\n--- Optimistic Consumption Scenario ---")
	fmt.Println("If storage is unavailable and cache is stale:")
	fmt.Println("  - System can allow up to 10% of quota optimistically")
	fmt.Println("  - For pro tier (10000 limit), that's up to 1000 units")
	fmt.Println("  - All optimistic consumption is tracked for reconciliation")

	// 9. Set up secondary storage with data
	fmt.Println("\n--- Secondary Storage Setup ---")
	err = secondaryStorage.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		fmt.Printf("Error setting entitlement in secondary: %v\n", err)
	} else {
		fmt.Println("Secondary storage configured with user entitlement")
	}

	fmt.Println("\n--- Summary ---")
	fmt.Println("Fallback strategies are configured and ready to handle storage failures.")
	fmt.Println("The system will gracefully degrade when storage is unavailable.")
	fmt.Println("Example completed successfully.")
}

