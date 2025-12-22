// Package main demonstrates using goquota with Redis storage
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	redisStorage "github.com/mihaimyh/goquota/storage/redis"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()

	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	defer client.Close()

	// Test Redis connection
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	fmt.Println("✓ Connected to Redis")

	// Create Redis storage with custom config
	storage, err := redisStorage.New(client, redisStorage.Config{
		KeyPrefix:      "goquota:",
		EntitlementTTL: 24 * time.Hour,
		UsageTTL:       0, // No expiration for usage
		MaxRetries:     3,
	})
	if err != nil {
		log.Fatalf("Failed to create Redis storage: %v", err)
	}
	fmt.Println("✓ Created Redis storage adapter")

	// Configure quota tiers
	config := goquota.Config{
		DefaultTier: "free",
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name:          "free",
				MonthlyQuotas: map[string]int{"api_calls": 100},
				DailyQuotas:   map[string]int{"api_calls": 10},
			},
			"pro": {
				Name:          "pro",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
				DailyQuotas:   map[string]int{"api_calls": 1000},
			},
		},
	}

	// Create quota manager
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	fmt.Println("✓ Created quota manager")

	// Set user entitlement
	userID := "demo_user"
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "pro",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}
	fmt.Printf("✓ Set user '%s' to 'pro' tier\n", userID)

	// Consume quota
	fmt.Println("\n--- Consuming Quota ---")
	for i := 1; i <= 5; i++ {
		newUsed, err := manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeMonthly)
		if err != nil {
			log.Fatalf("Failed to consume quota: %v", err)
		}
		fmt.Printf("Consumed 10 API calls. Total used: %d\n", newUsed)
	}

	// Get quota status
	usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to get quota: %v", err)
	}
	fmt.Printf("\n--- Quota Status ---\n")
	fmt.Printf("Used: %d / %d (%.1f%%)\n", usage.Used, usage.Limit, float64(usage.Used)/float64(usage.Limit)*100)
	fmt.Printf("Remaining: %d\n", usage.Limit-usage.Used)

	// Demonstrate tier change
	fmt.Println("\n--- Tier Change ---")
	fmt.Println("Downgrading to 'free' tier...")
	err = manager.ApplyTierChange(ctx, userID, "pro", "free", "api_calls")
	if err != nil {
		log.Fatalf("Failed to apply tier change: %v", err)
	}

	// Get updated quota status
	usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to get quota: %v", err)
	}
	fmt.Printf("New limit after downgrade: %d\n", usage.Limit)
	fmt.Printf("Used: %d / %d (%.1f%%)\n", usage.Used, usage.Limit, float64(usage.Used)/float64(usage.Limit)*100)

	// Demonstrate quota exceeded
	fmt.Println("\n--- Testing Quota Exceeded ---")
	_, err = manager.Consume(ctx, userID, "api_calls", 100, goquota.PeriodTypeMonthly)
	if err == goquota.ErrQuotaExceeded {
		fmt.Println("✓ Quota exceeded as expected")
	} else {
		fmt.Printf("Unexpected error: %v\n", err)
	}

	// Performance benchmark
	fmt.Println("\n--- Performance Benchmark ---")
	start := time.Now()
	iterations := 100
	for i := 0; i < iterations; i++ {
		_, err := manager.Consume(ctx, fmt.Sprintf("bench_user_%d", i), "api_calls", 1, goquota.PeriodTypeMonthly)
		if err != nil && err != goquota.ErrQuotaExceeded {
			log.Fatalf("Benchmark failed: %v", err)
		}
	}
	duration := time.Since(start)
	avgLatency := duration / time.Duration(iterations)
	fmt.Printf("Completed %d quota operations in %v\n", iterations, duration)
	fmt.Printf("Average latency: %v\n", avgLatency)

	fmt.Println("\n✓ Demo completed successfully!")
}
