package goquota_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/mihaimyh/goquota/storage/memory"
)

// Helper function to create a test manager with in-memory storage
func newTestManager() *goquota.Manager {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "explorer",
		CacheTTL:    time.Minute,
		Tiers: map[string]goquota.TierConfig{
			"explorer": {
				Name: "explorer",
				MonthlyQuotas: map[string]int{
					"audio_seconds": 0, // Free tier has no audio quota
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
			"fluent": {
				Name: "fluent",
				MonthlyQuotas: map[string]int{
					"audio_seconds": 18000, // 5 hours
				},
				DailyQuotas: map[string]int{
					"api_calls": 500,
				},
			},
		},
	}

	manager, _ := goquota.NewManager(storage, config)
	return manager
}

func TestNewManager(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "explorer",
		Tiers: map[string]goquota.TierConfig{
			"explorer": {Name: "explorer"},
		},
	}

	manager, err := goquota.NewManager(storage, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	// Test with nil storage
	_, err = goquota.NewManager(nil, config)
	if err != goquota.ErrStorageUnavailable {
		t.Errorf("Expected ErrStorageUnavailable, got %v", err)
	}
}

func TestManager_GetCurrentCycle(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up entitlement with subscription start 5 days ago
	subStart := time.Now().UTC().AddDate(0, 0, -5)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(subStart.Year(), subStart.Month(), subStart.Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Get current cycle
	period, err := manager.GetCurrentCycle(ctx, "user1")
	if err != nil {
		t.Fatalf("GetCurrentCycle failed: %v", err)
	}

	if period.Type != goquota.PeriodTypeMonthly {
		t.Errorf("Expected monthly period, got %v", period.Type)
	}

	// Verify cycle is approximately 30 days
	cycleDuration := period.End.Sub(period.Start)
	if cycleDuration < 28*24*time.Hour || cycleDuration > 31*24*time.Hour {
		t.Errorf("Unexpected cycle duration: %v", cycleDuration)
	}
}

func TestManager_GetQuota_NoUsage(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier entitlement
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Get quota for audio_seconds (no usage yet)
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	if usage.Used != 0 {
		t.Errorf("Expected 0 usage, got %d", usage.Used)
	}
	if usage.Limit != 3600 {
		t.Errorf("Expected limit 3600 (scholar tier), got %d", usage.Limit)
	}
	if usage.Tier != "scholar" {
		t.Errorf("Expected tier scholar, got %s", usage.Tier)
	}
}

func TestManager_Consume_Success(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume 100 seconds
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	if usage.Used != 100 {
		t.Errorf("Expected 100 usage, got %d", usage.Used)
	}
}

func TestManager_Consume_ExceedsLimit(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier (3600 seconds limit)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to consume more than limit
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 5000, goquota.PeriodTypeMonthly)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}
}

func TestManager_Consume_ZeroAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Consume 0 - should be no-op
	_, err := manager.Consume(ctx, "user1", "audio_seconds", 0, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Errorf("Consume(0) should succeed, got %v", err)
	}
}

func TestManager_Consume_NegativeAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Consume negative - should error
	_, err := manager.Consume(ctx, "user1", "audio_seconds", -100, goquota.PeriodTypeMonthly)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount, got %v", err)
	}
}

func TestManager_Consume_ExplorerTier_NoQuota(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up explorer tier (0 audio quota)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "explorer",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to consume - should fail (no quota for free tier)
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 1, goquota.PeriodTypeMonthly)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded for explorer tier, got %v", err)
	}
}

func TestManager_ApplyTierChange(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier and consume some quota
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day()-5, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume 1000 seconds
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 1000, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Upgrade to fluent tier
	err = manager.ApplyTierChange(ctx, "user1", "scholar", "fluent", "audio_seconds")
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Update entitlement
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "fluent",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day()-5, 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement update failed: %v", err)
	}

	// Get quota - should have prorated limit
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	// Limit should be higher than scholar (3600) but not full fluent (18000)
	// because it's prorated for remaining time in cycle
	if usage.Limit <= 3600 {
		t.Errorf("Expected prorated limit > 3600, got %d", usage.Limit)
	}
	if usage.Limit >= 18000 {
		t.Errorf("Expected prorated limit < 18000, got %d", usage.Limit)
	}
}

func TestManager_Consume_WithIdempotencyKey_Success(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "test-key-123"

	// First consumption with idempotency key
	newUsed1, err := manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}
	if newUsed1 != 100 {
		t.Errorf("Expected newUsed 100, got %d", newUsed1)
	}

	// Second consumption with same idempotency key - should return cached result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}
	if newUsed2 != 100 {
		t.Errorf("Expected cached newUsed 100, got %d", newUsed2)
	}

	// Verify usage was only consumed once
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 100 {
		t.Errorf("Expected usage 100 (consumed once), got %d", usage.Used)
	}
}

func TestManager_Consume_WithIdempotencyKey_DifferentKeys(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// First consumption with idempotency key 1
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey("key-1"))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}

	// Second consumption with different idempotency key - should consume again
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey("key-2"))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 200 {
		t.Errorf("Expected usage 200 (consumed twice), got %d", usage.Used)
	}
}

func TestManager_Consume_WithIdempotencyKey_WithoutKey(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume without idempotency key - should work normally
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Consume again without idempotency key - should consume again
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 200 {
		t.Errorf("Expected usage 200 (consumed twice), got %d", usage.Used)
	}
}

func TestManager_Consume_WithIdempotencyKey_QuotaExceeded(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier (3600 limit)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "test-key-exceeded"

	// Try to consume more than limit with idempotency key
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 5000, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}

	// Retry with same idempotency key - should still fail (not cached)
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 5000, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded on retry, got %v", err)
	}
}

func TestManager_Consume_WithIdempotencyKey_DifferentAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "test-key-diff-amount"

	// First consumption with idempotency key
	newUsed1, err := manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}
	if newUsed1 != 100 {
		t.Errorf("Expected newUsed 100, got %d", newUsed1)
	}

	// Second consumption with same idempotency key but different amount - should return cached result
	// This is expected behavior: idempotency keys are global, so same key = same result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 200, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}
	// Should return cached result from first request
	if newUsed2 != 100 {
		t.Errorf("Expected cached newUsed 100, got %d", newUsed2)
	}

	// Verify usage was only consumed once (100, not 200)
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 100 {
		t.Errorf("Expected usage 100 (consumed once), got %d", usage.Used)
	}
}

func TestManager_Consume_WithIdempotencyKey_DifferentResource(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "test-key-diff-resource"

	// First consumption for resource1
	_, err = manager.Consume(ctx, "user1", "api_calls", 10, goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}

	// Second consumption with same idempotency key but different resource - should return cached result
	// Idempotency keys are global, so same key = same cached result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}
	// Should return cached result from first request (which was for api_calls, not audio_seconds)
	// This returns the NewUsed from the first consumption record
	if newUsed2 != 10 {
		t.Errorf("Expected cached newUsed 10 (from first request), got %d", newUsed2)
	}
}

func TestManager_Consume_WithIdempotencyKey_EmptyString(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume with empty string idempotency key - should work normally (treated as no key)
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(""))
	if err != nil {
		t.Fatalf("Consume with empty idempotency key failed: %v", err)
	}

	// Consume again with empty string - should consume again
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(""))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 200 {
		t.Errorf("Expected usage 200 (consumed twice), got %d", usage.Used)
	}
}

func TestManager_Consume_WithIdempotencyKey_Concurrent(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user1",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "concurrent-key-123"
	const goroutines = 10

	done := make(chan int, goroutines)
	errors := make(chan error, goroutines)

	// Launch concurrent requests with same idempotency key
	for i := 0; i < goroutines; i++ {
		go func() {
			newUsed, err := manager.Consume(ctx, "user1", "audio_seconds", 10, goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
			done <- newUsed
			errors <- err
		}()
	}

	// Collect results
	var results []int
	for i := 0; i < goroutines; i++ {
		result := <-done
		err := <-errors
		if err != nil {
			t.Errorf("Concurrent Consume failed: %v", err)
		}
		results = append(results, result)
	}

	// All should return the same result (idempotent)
	expected := results[0]
	for i, result := range results {
		if result != expected {
			t.Errorf("Result %d differs: got %d, expected %d", i, result, expected)
		}
	}

	// Verify usage was only consumed once
	usage, err := manager.GetQuota(ctx, "user1", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected usage 10 (consumed once despite %d concurrent requests), got %d", goroutines, usage.Used)
	}
}
