package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
	// 1. Setup in-memory storage for this example
	storage := memory.New()

	// 2. Configure Manager with caching enabled
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 5},
			},
			"pro": {
				Name:          "pro",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  5 * time.Minute,
			UsageTTL:        30 * time.Second,
			MaxEntitlements: 1000,
			MaxUsage:        5000,
		},
	}

	manager, err := goquota.NewManager(storage, config)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	userID := "user_123"

	// 3. Set up an initial entitlement
	fmt.Println("Setting up entitlement for user_123...")
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// 4. Demonstrate performance (first call vs. subsequent calls)
	fmt.Println("\n--- Caching Performance Demo ---")

	for i := 1; i <= 3; i++ {
		start := time.Now()
		_, err := manager.Consume(ctx, userID, "api_calls", 1, goquota.PeriodTypeMonthly)
		duration := time.Since(start)

		if err != nil {
			fmt.Printf("Attempt %d: Error: %v\n", i, err)
		} else {
			fmt.Printf("Attempt %d: Quota consumed in %v\n", i, duration)
		}
	}

	// 5. Demonstrate cache invalidation
	fmt.Println("\n--- Cache Invalidation Demo ---")
	fmt.Println("Changing tier to 'free' (which has lower limit)...")

	// ApplyTierChange will trigger cache invalidation
	err = manager.ApplyTierChange(ctx, userID, "pro", "free", "api_calls")
	if err != nil {
		fmt.Printf("Error changing tier: %v\n", err)
	}

	// Also update the entitlement record
	manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "free",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})

	// Now try to consume more than the 'free' limit allows
	fmt.Println("Consuming 10 API calls on 'free' tier (limit is 5)...")
	_, err = manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeMonthly)
	if err == goquota.ErrQuotaExceeded {
		fmt.Println("Correctly rejected: Quota exceeded on new tier!")
	} else if err != nil {
		fmt.Printf("Unexpected error: %v\n", err)
	} else {
		fmt.Println("Warning: Quota should have been exceeded!")
	}

	// 6. Inspect cache stats
	// Note: In a real app, you'd use the Metrics interface we're about to implement.
	fmt.Println("\n--- Summary ---")
	fmt.Println("Caching layer is active and handling entitlement lookups and usage invalidations.")
	fmt.Println("Example completed successfully.")
}
