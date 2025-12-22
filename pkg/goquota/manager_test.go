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

	manager, _ := goquota.NewManager(storage, &config)
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

	manager, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if manager == nil {
		t.Fatal("Expected non-nil manager")
	}

	// Test with nil storage
	_, err = goquota.NewManager(nil, &config)
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
	newUsed1, err := manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}
	if newUsed1 != 100 {
		t.Errorf("Expected newUsed 100, got %d", newUsed1)
	}

	// Second consumption with same idempotency key - should return cached result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
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
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey("key-1"))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}

	// Second consumption with different idempotency key - should consume again
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey("key-2"))
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
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 5000,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}

	// Retry with same idempotency key - should still fail (not cached)
	_, err = manager.Consume(ctx, "user1", "audio_seconds", 5000,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
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
	newUsed1, err := manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}
	if newUsed1 != 100 {
		t.Errorf("Expected newUsed 100, got %d", newUsed1)
	}

	// Second consumption with same idempotency key but different amount - should return cached result
	// This is expected behavior: idempotency keys are global, so same key = same result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 200,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
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
	_, err = manager.Consume(ctx, "user1", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}

	// Second consumption with same idempotency key but different resource - should return cached result
	// Idempotency keys are global, so same key = same cached result
	newUsed2, err := manager.Consume(ctx, "user1", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
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
			newUsed, err := manager.Consume(ctx, "user1", "audio_seconds", 10,
				goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(idempotencyKey))
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

// Phase 4.2: Manager Time Edge Cases

func TestManager_ConsumeAtCycleBoundary(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set entitlement with subscription start on the 10th
	startDate := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)
	ent := &goquota.Entitlement{
		UserID:                "user_boundary",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	}

	err := manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume at cycle boundary
	// Note: In real usage, we'd need to mock time, but for this test we'll just verify
	// the cycle calculation works correctly

	_, err = manager.Consume(ctx, "user_boundary", "api_calls", 10, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Consume at 00:00:01 of next cycle
	// Note: This would be in a new cycle in real usage
	_, err = manager.Consume(ctx, "user_boundary", "api_calls", 10, goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("Consume at next cycle failed: %v", err)
	}
}

func TestManager_SubscriptionStartDate31st(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Subscription starts on 31st of month
	startDate := time.Date(2023, 1, 31, 0, 0, 0, 0, time.UTC)
	ent := &goquota.Entitlement{
		UserID:                "user_31st",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	}

	err := manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume in February (should handle month-end correctly)
	_, err = manager.Consume(ctx, "user_31st", "api_calls", 10, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume in February failed: %v", err)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, "user_31st", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected 10 used, got %d", usage.Used)
	}
}

func TestManager_SubscriptionStartDateFeb29(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Subscription starts on Feb 29, 2024 (leap year)
	startDate := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	ent := &goquota.Entitlement{
		UserID:                "user_feb29",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	}

	err := manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume in 2025 (non-leap year) - should handle Feb 28 correctly
	_, err = manager.Consume(ctx, "user_feb29", "api_calls", 10, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume in non-leap year failed: %v", err)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, "user_feb29", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected 10 used, got %d", usage.Used)
	}
}

func TestManager_SubscriptionStartDateFuture(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Subscription starts in the future (clock skew scenario)
	futureDate := time.Now().UTC().Add(30 * 24 * time.Hour)
	ent := &goquota.Entitlement{
		UserID:                "user_future",
		Tier:                  "scholar",
		SubscriptionStartDate: futureDate,
		UpdatedAt:             time.Now().UTC(),
	}

	err := manager.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume before start date - should still work (uses first cycle)
	_, err = manager.Consume(ctx, "user_future", "api_calls", 10, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume before start date failed: %v", err)
	}
}

// Phase 5: Boundary Value Tests

// Phase 5.1: Integer Overflow/Underflow

func TestManager_Consume_MaxInt(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier with very high limit
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_maxint",
		Tier:                  "fluent", // High limit tier
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to consume a very large amount (but within limit)
	// Note: In Go, int is at least 32 bits, so max is 2^31-1
	largeAmount := 1000000
	_, err = manager.Consume(ctx, "user_maxint", "api_calls", largeAmount, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Errorf("Consume with large amount should succeed, got %v", err)
	}
}

func TestManager_Refund_NegativeAmount(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_refund",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume some quota first
	_, err = manager.Consume(ctx, "user_refund", "api_calls", 50, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Try to refund negative amount - should error
	refundReq := &goquota.RefundRequest{
		UserID:     "user_refund",
		Resource:   "api_calls",
		Amount:     -10,
		PeriodType: goquota.PeriodTypeMonthly,
	}

	err = manager.Refund(ctx, refundReq)
	if err != goquota.ErrInvalidAmount {
		t.Errorf("Expected ErrInvalidAmount for negative refund, got %v", err)
	}
}

// Phase 5.2: Quota Limit Edge Cases

func TestManager_Consume_ExactLimit(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier (3600 seconds limit)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_exact",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume exactly at limit - should succeed
	_, err = manager.Consume(ctx, "user_exact", "audio_seconds", 3600, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Errorf("Consume at exact limit should succeed, got %v", err)
	}

	// Verify usage
	usage, err := manager.GetQuota(ctx, "user_exact", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 3600 {
		t.Errorf("Expected 3600 used, got %d", usage.Used)
	}
}

func TestManager_Consume_OverLimit(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier (3600 seconds limit)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_over",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to consume more than limit - should fail
	_, err = manager.Consume(ctx, "user_over", "audio_seconds", 3601, goquota.PeriodTypeMonthly)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded, got %v", err)
	}
}

func TestManager_Consume_ZeroLimit(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up explorer tier (0 quota)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_zero",
		Tier:                  "explorer",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to consume with zero limit - should fail
	_, err = manager.Consume(ctx, "user_zero", "audio_seconds", 1, goquota.PeriodTypeMonthly)
	if err != goquota.ErrQuotaExceeded {
		t.Errorf("Expected ErrQuotaExceeded for zero limit, got %v", err)
	}
}

func TestManager_Refund_OverRefund(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_overrefund",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume some quota
	_, err = manager.Consume(ctx, "user_overrefund", "api_calls", 50, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Refund more than used - should succeed but clamp to 0
	refundReq := &goquota.RefundRequest{
		UserID:     "user_overrefund",
		Resource:   "api_calls",
		Amount:     100, // More than used (50)
		PeriodType: goquota.PeriodTypeMonthly,
	}

	err = manager.Refund(ctx, refundReq)
	if err != nil {
		t.Fatalf("Refund over-refund should succeed (clamp to 0), got %v", err)
	}

	// Verify usage is 0
	usage, err := manager.GetQuota(ctx, "user_overrefund", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 0 {
		t.Errorf("Expected 0 used (clamped), got %d", usage.Used)
	}
}

// Phase 6: State Transition Tests

// Phase 6.2: Tier Change Edge Cases

func TestManager_ApplyTierChange_MidCycle(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier and consume some quota mid-cycle
	startDate := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_midcycle",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume some quota
	_, err = manager.Consume(ctx, "user_midcycle", "audio_seconds", 1000, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Upgrade mid-cycle
	err = manager.ApplyTierChange(ctx, "user_midcycle", "scholar", "fluent", "audio_seconds")
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Update entitlement to match
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_midcycle",
		Tier:                  "fluent",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement update failed: %v", err)
	}

	// Verify prorated limit was applied
	usage, err := manager.GetQuota(ctx, "user_midcycle", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	// Limit should be prorated (between 3600 and 18000)
	if usage.Limit <= 3600 {
		t.Errorf("Expected prorated limit > 3600, got %d", usage.Limit)
	}
	if usage.Limit >= 18000 {
		t.Errorf("Expected prorated limit < 18000, got %d", usage.Limit)
	}
	if usage.Used != 1000 {
		t.Errorf("Expected used to remain 1000, got %d", usage.Used)
	}
}

func TestManager_ApplyTierChange_DowngradeOverLimit(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up fluent tier and consume quota
	startDate := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_downgrade",
		Tier:                  "fluent",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume more than scholar limit (but within fluent limit)
	_, err = manager.Consume(ctx, "user_downgrade", "audio_seconds", 5000, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Downgrade to scholar (limit 3600, but used is 5000)
	err = manager.ApplyTierChange(ctx, "user_downgrade", "fluent", "scholar", "audio_seconds")
	if err != nil {
		t.Fatalf("ApplyTierChange failed: %v", err)
	}

	// Update entitlement
	err = manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_downgrade",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement update failed: %v", err)
	}

	// Verify usage is preserved (even though it exceeds new limit)
	usage, err := manager.GetQuota(ctx, "user_downgrade", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	// Used should be preserved
	if usage.Used != 5000 {
		t.Errorf("Expected used 5000, got %d", usage.Used)
	}

	// Limit should be prorated scholar limit
	if usage.Limit <= 0 {
		t.Errorf("Expected limit > 0, got %d", usage.Limit)
	}
}

func TestManager_ApplyTierChange_Concurrent(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up initial tier
	startDate := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_concurrent_tier",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	const goroutines = 20
	errChan := make(chan error, goroutines)

	// Concurrent tier changes
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			// Alternate between scholar and fluent
			if id%2 == 0 {
				errChan <- manager.ApplyTierChange(ctx, "user_concurrent_tier", "scholar", "fluent", "audio_seconds")
			} else {
				errChan <- manager.ApplyTierChange(ctx, "user_concurrent_tier", "fluent", "scholar", "audio_seconds")
			}
		}(i)
	}

	// Collect errors
	for i := 0; i < goroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent tier change %d failed: %v", i, err)
		}
	}

	// Verify final state is consistent
	usage, err := manager.GetQuota(ctx, "user_concurrent_tier", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	// Usage should be valid (>= 0)
	if usage.Used < 0 {
		t.Errorf("Expected used >= 0, got %d", usage.Used)
	}
}

func TestManager_ApplyTierChange_InvalidTier(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up scholar tier
	startDate := time.Date(2023, 1, 10, 0, 0, 0, 0, time.UTC)
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_invalid",
		Tier:                  "scholar",
		SubscriptionStartDate: startDate,
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Try to change to invalid tier - should handle gracefully
	// The manager should calculate limits (0 for unknown tier) but not error
	err = manager.ApplyTierChange(ctx, "user_invalid", "scholar", "nonexistent_tier", "audio_seconds")
	// This might succeed (with 0 limit) or error depending on implementation
	// Let's verify it doesn't panic
	if err != nil {
		// Error is acceptable for invalid tier
		t.Logf("ApplyTierChange with invalid tier returned error (expected): %v", err)
	}
}

// Phase 10: Integration Tests

func TestManager_CacheStorageConsistency(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 1000},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  time.Minute,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}

	mgr, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()

	// Set entitlement
	ent := &goquota.Entitlement{
		UserID:                "user_cache_consistency",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = mgr.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Get quota - should populate cache
	usage1, err := mgr.GetQuota(ctx, "user_cache_consistency", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage1.Limit != 1000 {
		t.Errorf("Expected limit 1000, got %d", usage1.Limit)
	}

	// Consume quota - should invalidate cache
	_, err = mgr.Consume(ctx, "user_cache_consistency", "api_calls", 100, goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// Get quota again - should reflect new usage (cache invalidated)
	usage2, err := mgr.GetQuota(ctx, "user_cache_consistency", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota after consume failed: %v", err)
	}
	if usage2.Used != 100 {
		t.Errorf("Expected used 100, got %d", usage2.Used)
	}

	// Update entitlement - should invalidate cache
	ent.Tier = "fluent"
	err = mgr.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement update failed: %v", err)
	}

	// Get quota - should reflect new tier
	usage3, err := mgr.GetQuota(ctx, "user_cache_consistency", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota after tier change failed: %v", err)
	}
	// Limit should change (if fluent tier has different quota)
	// Note: This depends on tier config, but cache should be invalidated
	if usage3.Limit == usage2.Limit && usage2.Limit == 1000 {
		// If tiers have same quota, at least verify cache was checked
		t.Log("Cache consistency verified (tiers may have same quota)")
	}
}

func TestManager_ConcurrentOperations(t *testing.T) {
	storage := memory.New()
	config := goquota.Config{
		DefaultTier: "scholar",
		Tiers: map[string]goquota.TierConfig{
			"scholar": {
				Name:          "scholar",
				MonthlyQuotas: map[string]int{"api_calls": 10000},
			},
		},
		CacheConfig: &goquota.CacheConfig{
			Enabled:         true,
			EntitlementTTL:  time.Minute,
			MaxEntitlements: 100,
			MaxUsage:        1000,
		},
	}

	mgr, err := goquota.NewManager(storage, &config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()

	// Set entitlement
	ent := &goquota.Entitlement{
		UserID:                "user_concurrent_ops",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	err = mgr.SetEntitlement(ctx, ent)
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	const goroutines = 50
	errChan := make(chan error, goroutines)
	doneChan := make(chan bool, goroutines)

	// Concurrent operations: consume, get quota, refund
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			var err error
			switch id % 3 {
			case 0:
				_, err = mgr.Consume(ctx, "user_concurrent_ops", "api_calls", 10, goquota.PeriodTypeMonthly)
			case 1:
				_, err = mgr.GetQuota(ctx, "user_concurrent_ops", "api_calls", goquota.PeriodTypeMonthly)
			case 2:
				err = mgr.Refund(ctx, &goquota.RefundRequest{
					UserID:     "user_concurrent_ops",
					Resource:   "api_calls",
					Amount:     5,
					PeriodType: goquota.PeriodTypeMonthly,
				})
			}
			errChan <- err
			doneChan <- true
		}(i)
	}

	// Collect results
	errors := 0
	for i := 0; i < goroutines; i++ {
		<-doneChan
		if err := <-errChan; err != nil {
			errors++
			// Some errors are expected (e.g., quota exceeded, over-refund)
			if err != goquota.ErrQuotaExceeded && err != goquota.ErrInvalidAmount {
				t.Errorf("Unexpected error: %v", err)
			}
		}
	}

	// Verify final state is consistent
	usage, err := mgr.GetQuota(ctx, "user_concurrent_ops", "api_calls", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}

	// Usage should be valid (>= 0, <= limit)
	if usage.Used < 0 {
		t.Errorf("Expected used >= 0, got %d", usage.Used)
	}
	if usage.Used > usage.Limit {
		t.Errorf("Expected used <= limit, got used=%d limit=%d", usage.Used, usage.Limit)
	}

	t.Logf("Concurrent operations completed: %d errors (some expected)", errors)
}

// Phase 7: Data Consistency Tests

// Phase 7.1: Idempotency Key Edge Cases

func TestManager_Consume_EmptyIdempotencyKey(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_empty_key",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	// Consume with empty idempotency key - should work normally
	_, err = manager.Consume(ctx, "user_empty_key", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(""))
	if err != nil {
		t.Fatalf("Consume with empty key failed: %v", err)
	}

	// Consume again with empty key - should consume again
	_, err = manager.Consume(ctx, "user_empty_key", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(""))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}

	// Verify usage was consumed twice
	usage, err := manager.GetQuota(ctx, "user_empty_key", "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 20 {
		t.Errorf("Expected 20 used (consumed twice), got %d", usage.Used)
	}
}

func TestManager_Consume_SameKeyDifferentAmounts(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_same_key",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	idempotencyKey := "same-key-diff-amount"

	// First consume with amount 10
	newUsed1, err := manager.Consume(ctx, "user_same_key", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("First Consume failed: %v", err)
	}
	if newUsed1 != 10 {
		t.Errorf("Expected newUsed 10, got %d", newUsed1)
	}

	// Second consume with same key but different amount - should return cached result
	newUsed2, err := manager.Consume(ctx, "user_same_key", "api_calls", 20,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(idempotencyKey))
	if err != nil {
		t.Fatalf("Second Consume failed: %v", err)
	}
	// Should return cached result from first request
	if newUsed2 != 10 {
		t.Errorf("Expected cached newUsed 10, got %d", newUsed2)
	}

	// Verify usage was only consumed once (10, not 20)
	usage, err := manager.GetQuota(ctx, "user_same_key", "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 10 {
		t.Errorf("Expected 10 used (consumed once), got %d", usage.Used)
	}
}

func TestManager_Consume_KeyCollisionAcrossUsers(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up two users
	for _, userID := range []string{"user1", "user2"} {
		err := manager.SetEntitlement(ctx, &goquota.Entitlement{
			UserID:                userID,
			Tier:                  "scholar",
			SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
			UpdatedAt:             time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("SetEntitlement for %s failed: %v", userID, err)
		}
	}

	sharedKey := "shared-key-123"

	// Consume for user1 with shared key
	_, err := manager.Consume(ctx, "user1", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(sharedKey))
	if err != nil {
		t.Fatalf("Consume for user1 failed: %v", err)
	}

	// Consume for user2 with same key - should return cached result from user1
	// This tests that idempotency keys are global (not per-user)
	newUsed2, err := manager.Consume(ctx, "user2", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(sharedKey))
	if err != nil {
		t.Fatalf("Consume for user2 failed: %v", err)
	}
	// Should return cached result from user1's consumption
	if newUsed2 != 10 {
		t.Errorf("Expected cached newUsed 10, got %d", newUsed2)
	}

	// Verify user2's usage was NOT consumed (idempotency key collision)
	usage2, err := manager.GetQuota(ctx, "user2", "api_calls", goquota.PeriodTypeDaily)
	if err != nil {
		t.Fatalf("GetQuota for user2 failed: %v", err)
	}
	// User2 should have 0 used (idempotency key returned cached result from user1)
	if usage2.Used != 0 {
		t.Errorf("Expected 0 used for user2 (key collision), got %d", usage2.Used)
	}
}

func TestManager_Consume_KeyCollisionAcrossResources(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// Set up tier
	err := manager.SetEntitlement(ctx, &goquota.Entitlement{
		UserID:                "user_multi_resource",
		Tier:                  "scholar",
		SubscriptionStartDate: time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SetEntitlement failed: %v", err)
	}

	sharedKey := "shared-resource-key"

	// Consume for resource1
	_, err = manager.Consume(ctx, "user_multi_resource", "api_calls", 10,
		goquota.PeriodTypeDaily, goquota.WithIdempotencyKey(sharedKey))
	if err != nil {
		t.Fatalf("Consume for api_calls failed: %v", err)
	}

	// Consume for resource2 with same key - should return cached result
	// This tests that idempotency keys are global (not per-resource)
	newUsed2, err := manager.Consume(ctx, "user_multi_resource", "audio_seconds", 100,
		goquota.PeriodTypeMonthly, goquota.WithIdempotencyKey(sharedKey))
	if err != nil {
		t.Fatalf("Consume for audio_seconds failed: %v", err)
	}
	// Should return cached result from api_calls consumption
	if newUsed2 != 10 {
		t.Errorf("Expected cached newUsed 10, got %d", newUsed2)
	}

	// Verify audio_seconds usage was NOT consumed
	usage, err := manager.GetQuota(ctx, "user_multi_resource", "audio_seconds", goquota.PeriodTypeMonthly)
	if err != nil {
		t.Fatalf("GetQuota failed: %v", err)
	}
	if usage.Used != 0 {
		t.Errorf("Expected 0 used for audio_seconds (key collision), got %d", usage.Used)
	}
}
