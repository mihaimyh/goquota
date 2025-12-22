// Package main demonstrates how to use goquota with Firestore storage
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/mihaimyh/goquota/pkg/goquota"
	firestoreStorage "github.com/mihaimyh/goquota/storage/firestore"
)

func main() {
	ctx := context.Background()

	// Initialize Firestore client
	projectID := "your-project-id"
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create Firestore client: %v", err)
	}
	defer client.Close()

	// Create Firestore storage adapter
	storage, err := firestoreStorage.New(client, firestoreStorage.Config{
		EntitlementsCollection: "billing_entitlements",
		UsageCollection:        "billing_usage",
	})
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}

	// Configure quota tiers
	config := goquota.Config{
		DefaultTier: "explorer",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: "explorer",
				MonthlyQuotas: map[string]int{
					"audio_seconds": 0, // Free tier
				},
				DailyQuotas: map[string]int{
					"api_calls": 50,
				},
			},
			"scholar": {
				Name: "scholar",
				MonthlyQuotas: map[string]int{
					"audio_seconds": 3600, // 1 hour
				},
				DailyQuotas: map[string]int{
					"api_calls": 500,
				},
			},
		},
	}

	// Create quota manager
	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Example: Set user entitlement
	userID := "user123"
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                userID,
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("Failed to set entitlement: %v", err)
	}
	fmt.Println("✓ Entitlement set")

	// Example: Get current quota
	usage, err := manager.GetQuota(ctx, userID, "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		log.Fatalf("Failed to get quota: %v", err)
	}
	fmt.Printf(" Updated quota: %d/%d seconds used\n", usage.Used, usage.Limit)

	// Example: Apply tier change
	err = manager.ApplyTierChange(ctx, userID, "scholar", "scholar", "audio_seconds")
	if err != nil {
		log.Fatalf("Failed to apply tier change: %v", err)
	}
	fmt.Println(" Tier change applied with proration")
}
