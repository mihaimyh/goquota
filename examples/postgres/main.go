package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/postgres"
)

func main() {
	// Get connection string from environment or use default
	connString := os.Getenv("POSTGRES_DSN")
	if connString == "" {
		connString = "postgres://postgres:postgres@localhost:5432/goquota?sslmode=disable"
	}

	// Create PostgreSQL storage adapter
	ctx := context.Background()
	config := postgres.DefaultConfig()
	config.ConnectionString = connString
	config.CleanupEnabled = true
	config.CleanupInterval = 1 * time.Hour
	config.RecordTTL = 7 * 24 * time.Hour // 7 days

	storage, err := postgres.New(ctx, config)
	if err != nil {
		log.Fatalf("Failed to create PostgreSQL storage: %v", err)
	}
	defer storage.Close()

	// Create quota manager
	managerConfig := &goquota.Config{
		Tiers: map[string]goquota.TierConfig{
			"free": {
				Name: "free",
				MonthlyQuotas: map[string]int{
					"api_calls": 1000,
				},
			},
			"premium": {
				Name: "premium",
				MonthlyQuotas: map[string]int{
					"api_calls": 10000,
				},
			},
		},
		DefaultTier: "free",
	}

	manager, err := goquota.NewManager(storage, managerConfig)
	if err != nil {
		//nolint:gocritic // Example code: intentional exit on error
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Set user entitlement
	userID := "user123"
	entitlement := &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "premium",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}

	err = manager.SetEntitlement(ctx, entitlement)
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}

	fmt.Printf("Set entitlement for user %s: tier=%s\n", userID, entitlement.Tier)

	// Consume quota
	newUsed, err := manager.Consume(ctx, userID, "api_calls", 10, goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to consume quota: %v", err)
	}

	fmt.Printf("Consumed 10 API calls. New total used: %d\n", newUsed)

	// Get current quota
	quota, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to get quota: %v", err)
	}

	fmt.Printf("Current quota: %d/%d (%.1f%%)\n",
		quota.Used, quota.Limit,
		float64(quota.Used)/float64(quota.Limit)*100)

	// Try to consume more than limit
	_, err = manager.Consume(ctx, userID, "api_calls", 99999, goquota.PeriodTypeMonthly)
	if err == goquota.ErrQuotaExceeded {
		fmt.Println("Quota exceeded (as expected)")
	} else if err != nil {
		log.Fatalf("Unexpected error: %v", err)
	}

	// Refund quota
	refundReq := &goquota.RefundRequest{
		UserID:         userID,
		Resource:       "api_calls",
		Amount:         5,
		PeriodType:     goquota.PeriodTypeMonthly,
		IdempotencyKey: "refund-1",
		Reason:         "Test refund",
	}
	err = manager.Refund(ctx, refundReq)
	if err != nil {
		log.Fatalf("Failed to refund quota: %v", err)
	}

	fmt.Println("Refunded 5 API calls")

	// Final quota check
	quota, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to get quota: %v", err)
	}

	fmt.Printf("Final quota: %d/%d\n", quota.Used, quota.Limit)

	fmt.Println("\nExample completed successfully!")
	fmt.Println("\nNote: Rate limiting is handled in-memory per instance.")
	fmt.Println("      Monthly quotas are synchronized globally via PostgreSQL.")
}
