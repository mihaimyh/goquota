// Package main demonstrates basic goquota usage
package main

import (
"context"
"fmt"
"log"
"time"

"github.com/mihaimyh/goquota/pkg/goquota"
"github.com/mihaimyh/goquota/storage/memory"
)

func main() {
fmt.Println("=== goquota Basic Example ===\n")

// 1. Create in-memory storage
storage := memory.New()

// 2. Configure quota tiers
config := goquota.Config{
DefaultTier: "free",
CacheTTL:    time.Minute,
Tiers: map[string]goquota.TierConfig{
"free": {
Name: "free",
MonthlyQuotas: map[string]int{
"api_calls": 100,
},
},
"pro": {
Name: "pro",
MonthlyQuotas: map[string]int{
"api_calls": 10000,
},
},
},
}

// 3. Create quota manager
manager, err := goquota.NewManager(storage, config)
if err != nil {
log.Fatalf("Failed to create manager: %v", err)
}

ctx := context.Background()
userID := "user123"

// 4. Set user entitlement
fmt.Println("Setting user entitlement to 'pro' tier...")
err = manager.SetEntitlement(ctx, &goquota.Entitlement{
UserID:                userID,
Tier:                  "pro",
SubscriptionStartDate: time.Now().UTC(),
UpdatedAt:             time.Now().UTC(),
})
if err != nil {
log.Fatalf("Failed to set entitlement: %v", err)
}

// 5. Get current billing cycle
period, err := manager.GetCurrentCycle(ctx, userID)
if err != nil {
log.Fatalf("Failed to get cycle: %v", err)
}
fmt.Printf("Current billing cycle: %s to %s\n\n", 
period.Start.Format("2006-01-02"), 
period.End.Format("2006-01-02"))

// 6. Check initial quota
usage, err := manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
if err != nil {
log.Fatalf("Failed to get quota: %v", err)
}
fmt.Printf("Initial quota: %d/%d API calls used\n", usage.Used, usage.Limit)

// 7. Consume some quota
fmt.Println("\nConsuming 50 API calls...")
err = manager.Consume(ctx, userID, "api_calls", 50, goquota.PeriodTypeMonthly)
if err != nil {
log.Fatalf("Failed to consume quota: %v", err)
}

// 8. Check quota again
usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
if err != nil {
log.Fatalf("Failed to get quota: %v", err)
}
fmt.Printf("After consumption: %d/%d API calls used\n", usage.Used, usage.Limit)

// 9. Try to exceed quota
fmt.Println("\nTrying to consume 20,000 API calls (exceeds limit)...")
err = manager.Consume(ctx, userID, "api_calls", 20000, goquota.PeriodTypeMonthly)
if err == goquota.ErrQuotaExceeded {
fmt.Println(" Quota exceeded error caught correctly")
} else {
log.Fatalf("Expected quota exceeded error, got: %v", err)
}

// 10. Apply tier change (downgrade to free)
fmt.Println("\nDowngrading to 'free' tier...")
err = manager.ApplyTierChange(ctx, userID, "pro", "free", "api_calls")
if err != nil {
log.Fatalf("Failed to apply tier change: %v", err)
}

// Update entitlement
err = manager.SetEntitlement(ctx, &goquota.Entitlement{
UserID:                userID,
Tier:                  "free",
SubscriptionStartDate: time.Now().UTC(),
UpdatedAt:             time.Now().UTC(),
})
if err != nil {
log.Fatalf("Failed to update entitlement: %v", err)
}

// 11. Check prorated quota
usage, err = manager.GetQuota(ctx, userID, "api_calls", goquota.PeriodTypeMonthly)
if err != nil {
log.Fatalf("Failed to get quota: %v", err)
}
fmt.Printf("After downgrade: %d/%d API calls (prorated)\n", usage.Used, usage.Limit)

fmt.Println("\n=== Example Complete ===")
}